#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
Quake 空间资产探测「探针」工具
================================
直接调用 360Quake 搜索接口查询 `domain:"xxx"`，忠实输出每条资产的原始字段，
用于排查「收集到的 URL 与预期不一致」的问题：
  - 列出每条资产：url(http_load_url)、hostname、ip:port、service.name、transport、title
  - 统计有多少条资产【没有】URL，以及有多少条被黑名单过滤
  - 黑名单：python_probe/blacklist.txt（或用 --blacklist 指定文件），按“包含”匹配 URL/子域名
  - 导出：URL 全量表 / 子域名去重表 / 原始响应 JSON（可 --replay 离线重放）

用法示例：
  python quake_probe.py --token-file data/api.txt --domain js.10086.cn
  python quake_probe.py --token 你的token --query 'domain:"js.10086.cn"' --max 500
  python quake_probe.py --sample 3 --domain js.10086.cn                 # 打印前3条原始JSON
  python quake_probe.py --blacklist my_blacklist.txt --domain js.10086.cn  # 指定黑名单文件
  python quake_probe.py --replay results_xxx_raw.json                   # 离线重放，不消耗点数
"""

import argparse
import csv
import ipaddress
import json
import os
import ssl
import sys
import time
import urllib.error
import urllib.request
from datetime import datetime
from urllib.parse import urlparse

DEFAULT_BASE = "https://quake.360.net"
DEFAULT_PAGE_SIZE = 1000
AUTH_MARKERS = ("/quake/login", "login")

_probe_args = None  # 供 api_search 读取 CLI 参数（proxy/insecure）


# ---------------- 输入 Token ----------------

def resolve_token(args):
    tok = args.token or ""
    if not tok and args.token_file:
        try:
            with open(args.token_file, encoding="utf-8") as f:
                for line in f:
                    line = line.strip()
                    if line and not line.startswith("#"):
                        tok = line
                        break
        except OSError as e:
            sys.exit("读取 token 文件失败: %s" % e)
    if not tok:
        tok = os.environ.get("QUAKE_TOKEN", "")
    if not tok:
        sys.exit("未找到 Token：请用 --token / --token-file 指定，或设置环境变量 QUAKE_TOKEN")
    return tok


# ---------------- 黑名单 ----------------

def default_blacklist():
    return os.path.join(os.path.dirname(os.path.abspath(__file__)), "blacklist.txt")


def load_blacklist(path):
    """读取黑名单（小写化）；按“包含”匹配 URL 或子域名。"""
    terms = []
    if path and os.path.exists(path):
        with open(path, encoding="utf-8") as f:
            for line in f:
                t = line.strip().lower()
                if t and not t.startswith("#"):
                    terms.append(t)
    return terms


def blacklisted(u, sub, terms):
    if not terms:
        return False
    lu, ls = u.lower(), sub.lower()
    for t in terms:
        if t in lu or t in ls:
            return True
    return False


# ---------------- 网络请求 ----------------

def _open(req, timeout, insecure=False, proxy=""):
    """按参数构建 opener 并发起请求（支持代理与关闭证书校验）。"""
    handlers = []
    if proxy:
        handlers.append(urllib.request.ProxyHandler({"http": proxy, "https": proxy}))
    if insecure:
        ctx = ssl.create_default_context()
        ctx.check_hostname = False
        ctx.verify_mode = ssl.CERT_NONE
        handlers.append(urllib.request.HTTPSHandler(context=ctx))
    opener = urllib.request.build_opener(*handlers) if handlers else urllib.request.build_opener()
    return opener.open(req, timeout=timeout)


def _do_request(req, timeout, insecure=False, proxy=""):
    """实际发送请求；遇重定向到登录页判定为未授权。"""
    resp = _open(req, timeout, insecure=insecure, proxy=proxy)
    final_url = resp.geturl()
    if any(m in final_url for m in AUTH_MARKERS):
        return -1, {"message": "未授权：API 被重定向到登录页（token 无效或已过期）"}
    raw = resp.read().decode("utf-8", "replace")
    try:
        return resp.status, json.loads(raw)
    except Exception:
        return resp.status, {"message": "响应不是 JSON: %s" % raw[:300]}


def do_request(req, timeout):
    """发送请求：遇系统 CA 证书校验失败时，自动降级为不校验证书重试一次（保留代理）。"""
    proxy = getattr(_probe_args, "proxy", "")
    try:
        return _do_request(req, timeout, proxy=proxy)
    except urllib.error.URLError as e:
        msg = str(e)
        if "CERTIFICATE_VERIFY_FAILED" in msg or "certificate verify failed" in msg:
            if not getattr(_probe_args, "insecure", False):
                print("[warn] 系统 CA 证书校验失败，已自动改用不校验证书方式重试（也可显式加 --insecure）")
                return _do_request(req, timeout, insecure=True, proxy=proxy)
        raise


def api_search(token, base, query, start, size, timeout=60):
    url = base.rstrip("/") + "/api/v3/search/quake_service"
    body = json.dumps({"query": query, "start": start, "size": size}).encode("utf-8")
    req = urllib.request.Request(url, data=body, method="POST")
    req.add_header("Content-Type", "application/json")
    req.add_header("X-QuakeToken", token)
    try:
        return do_request(req, timeout)
    except urllib.error.HTTPError as e:
        raw = e.read().decode("utf-8", "replace")
        try:
            js = json.loads(raw)
        except Exception:
            js = {"message": "HTTP %d: %s" % (e.code, raw[:300])}
        if e.code == 401:
            return e.code, {"message": "鉴权失败(401)：token 无效或已过期"}
        if e.code == 429:
            return e.code, {"message": "Quake 限流(429)"}
        return e.code, js
    except (urllib.error.URLError, ssl.SSLError, OSError) as e:
        return -2, {"message": "请求失败: %s" % e}


# ---------------- 数据提取 ----------------

def extract_item(it):
    """把一条资产压缩为行记录。

    约定：URL 严格取原始响应 service.http.http_load_url（数组，取首个元素），
    未做任何拼接；兼容旧的 service.http.load_url 字符串字段。
    """
    svc = it.get("service") or {}
    http = svc.get("http") or {}
    hostname = it.get("hostname") or ""
    if isinstance(hostname, list):
        hostname = hostname[0] if hostname else ""
    if not isinstance(hostname, str):
        hostname = str(hostname)
    title = http.get("title") or ""
    if not isinstance(title, str):
        title = str(title)
    load_urls = http.get("http_load_url") or []
    if isinstance(load_urls, str):
        load_urls = [load_urls]
    url = next((str(u).strip() for u in load_urls if str(u).strip()), "")
    if not url:
        url = (http.get("load_url") or "").strip()
    return {
        "url": url,
        "ip": it.get("ip") or "",
        "port": it.get("port") or 0,
        "hostname": hostname,
        "service": svc.get("name") or "",
        "protocol": it.get("transport") or "",
        "title": title.replace("\n", " ").replace("\r", " "),
    }


def is_ip_literal(s):
    try:
        ipaddress.ip_address(s)
        return True
    except Exception:
        return False


def row_host(r):
    """行记录的子域名依据：URL 的 hostname（兼容无 scheme 的裸主机名），
    缺失时退回 hostname 字段，绝不用 IP。"""
    u = r["url"]
    if u:
        if "://" not in u:
            u = "//" + u
        hu = urlparse(u).hostname
        if hu:
            return hu.lower()
    return (r["hostname"] or "").lower()


def query_text(args):
    if args.query:
        return args.query
    return 'domain: "%s"' % (args.domain or "js.10086.cn")


# ---------------- 收集 ----------------

def collect(args):
    if args.replay:
        with open(args.replay, encoding="utf-8") as f:
            payload = json.load(f)
        if isinstance(payload, dict):
            code = payload.get("code")
            if code not in (0, None, ""):
                sys.exit("重放数据非成功响应：code=%s message=%s" % (code, payload.get("message", "")))
            data = payload.get("data") or []
        elif isinstance(payload, list):
            data = payload
        else:
            sys.exit("无法识别的重放数据格式")
        rows = [extract_item(it) for it in data]
        return "[replay %s]" % os.path.basename(args.replay), rows, data

    token = resolve_token(args)
    base = args.base.rstrip("/")
    query = query_text(args)
    token_mask = (token[:6] + "****") if len(token) > 8 else "****"
    print("== 查询 ==")
    print("  query : %s" % query)
    print("  token : %s (已脱敏)   接口: %s/api/v3/search/quake_service" % (token_mask, base))
    if args.proxy:
        print("  proxy : %s" % args.proxy)

    rows, raw_all = [], []
    start, page_no = 0, 1
    while start < args.max:
        status, js = api_search(token, base, query, start, args.page_size, args.timeout)
        if not isinstance(js, dict):
            js = {"message": str(js)}
        code_val = js.get("code")
        msg = js.get("message", "")
        if status != 200 or code_val not in (0, None, ""):
            print("\n[!] 接口返回异常: HTTP=%s code=%s message=%s" % (status, code_val, msg))
            if status in (401, -1) or "未授权" in msg or "鉴权" in msg:
                print("    原因通常是 token 无效/已过期，或未走 --token-file 指定正确凭证。")
            elif not args.query and not args.replay:
                print("    若提示查询语法问题，可用 --query 自定义语法，如 --query 'service:http'。")
            break
        data = js.get("data") or []
        raw_all.extend(data)
        for it in data:
            rows.append(extract_item(it))
        print("  第 %d 页: +%d 条 (累计 %d)，start=%d" % (page_no, len(data), len(rows), start))
        if len(data) < args.page_size:
            break
        start += args.page_size
        page_no += 1
        time.sleep(0.3)  # 温和限速
    return query, rows, raw_all


# ---------------- 输出 ----------------

def write_csv(path, header, list_of_rows):
    with open(path, "w", newline="", encoding="utf-8-sig") as f:
        w = csv.writer(f)
        w.writerow(header)
        for r in list_of_rows:
            w.writerow(r)


def outputs(args, rows, raw_all, terms):
    default_out = os.path.join(os.path.dirname(os.path.abspath(__file__)), "results")
    out_dir = args.out or os.path.join(default_out, args.domain or "replay")
    os.makedirs(out_dir, exist_ok=True)
    ts = datetime.now().strftime("%Y%m%d_%H%M%S")
    name = "replay" if args.replay else (args.domain or "query")
    safe = "".join(c if c.isalnum() or c in "-_." else "_" for c in name)

    # 黑名单过滤：命中 URL 或子域名的整行剔除
    keep, filtered = [], 0
    for r in rows:
        if blacklisted(r["url"], row_host(r), terms):
            filtered += 1
            continue
        keep.append(r)

    urls_csv = os.path.join(out_dir, "%s_urls_%s.csv" % (safe, ts))
    write_csv(urls_csv,
              ["url", "ip", "port", "hostname", "service", "protocol", "title", "has_url"],
              [[r["url"], r["ip"], r["port"], r["hostname"], r["service"], r["protocol"], r["title"],
                "yes" if r["url"] else "no"] for r in keep])

    subs, seen = [], set()
    for r in keep:
        hu = row_host(r)
        if not hu or is_ip_literal(hu):  # IP 字面量不是子域名，排除
            continue
        if hu in seen:
            continue
        seen.add(hu)
        subs.append(hu)
    sub_csv = os.path.join(out_dir, "%s_subdomain_%s.csv" % (safe, ts))
    write_csv(sub_csv, ["subdomain"], [[s] for s in subs])

    raw_json = os.path.join(out_dir, "%s_raw_%s.json" % (safe, ts))
    with open(raw_json, "w", encoding="utf-8") as f:
        json.dump(raw_all, f, ensure_ascii=False, indent=1)

    return urls_csv, sub_csv, raw_json, subs, filtered


def summary(query, rows, subs, filtered):
    n = len(rows)
    with_url = sum(1 for r in rows if r["url"])
    without_url = n - with_url
    non_http = sum(1 for r in rows if r["service"] not in ("http", "https", ""))
    no_svc = sum(1 for r in rows if not r["service"])
    print("\n== 结果统计 ==")
    print("  query           : %s" % query)
    print("  资产总数         : %d" % n)
    print("  有 http_load_url: %d  （会出现在 URL 表中）" % with_url)
    print("  无 URL          : %d  （不在 URL 表中，很可能就是差异来源）" % without_url)
    print("  其中非 http/https : %d  无 service.name 的: %d" % (non_http, no_svc))
    print("  黑名单过滤       : %d" % filtered)
    print("  从 URL 提取去重子域名 : %d" % len(subs))
    if without_url:
        print("\n  [提示] 无 URL 的资产只有 ip/port/hostname 等，Quake 没有返回 URL 字段；")
        print("         若要把这些也收录为子域名，需改从 hostname 字段提取，而不是从 URL。")


def main():
    global _probe_args
    ap = argparse.ArgumentParser(description="Quake 空间资产探测探针（排查 URL 导出差异 + 黑名单过滤）")
    ap.add_argument("--token", default="", help="360Quake API Token")
    ap.add_argument("--token-file", default="", help="从文件读取 Token（首个非#非空行，如 data/api.txt）")
    ap.add_argument("--domain", default="", help='查询域名，等价于 domain:"域名"（默认 js.10086.cn）')
    ap.add_argument("--query", default="", help="自定义完整查询语法（优先级高于 --domain）")
    ap.add_argument("--max", type=int, default=50000, help="最多拉取条数（默认50000）")
    ap.add_argument("--page-size", type=int, default=DEFAULT_PAGE_SIZE, help="单页条数（默认1000，Quake没有上限，实测支持10000）")
    ap.add_argument("--base", default=DEFAULT_BASE, help="API 域名根，如 https://quake.360.net")
    ap.add_argument("--out", default="", help="输出目录（默认 ./results/<域名>/）")
    ap.add_argument("--timeout", type=int, default=60, help="请求超时秒数")
    ap.add_argument("--sample", type=int, default=0, help="打印前 N 条原始资产 JSON（查看真实字段，默认0）")
    ap.add_argument("--replay", default="", help="离线重放 _raw_*.json，不联网不消耗点数")
    ap.add_argument("--proxy", default="", help="HTTP/HTTPS 代理，如 http://127.0.0.1:8080")
    ap.add_argument("--blacklist", default="", help="黑名单文件（默认 python_probe/blacklist.txt；按“包含”匹配 URL/子域名）")
    ap.add_argument("--insecure", action="store_true", help="关闭 SSL 证书校验（个别网络需要）")
    args = ap.parse_args()
    _probe_args = args

    if not args.domain and not args.query and not args.replay:
        args.domain = "js.10086.cn"

    terms = load_blacklist(args.blacklist or default_blacklist())
    if terms:
        print("黑名单（%d 条）: %s" % (len(terms), ", ".join(terms)))

    query, rows, raw_all = collect(args)

    if args.sample > 0 and raw_all:
        print("\n== 原始资产样例（前 %d 条，核对真实字段） ==" % args.sample)
        print(json.dumps(raw_all[:args.sample], ensure_ascii=False, indent=1)[:4000])

    urls_csv, sub_csv, raw_json, subs, filtered = outputs(args, rows, raw_all, terms)
    summary(query, rows, subs, filtered)

    print("\n== 输出文件 ==")
    print("  URL 全量表   : %s" % urls_csv)
    print("  子域名去重表 : %s" % sub_csv)
    print("  原始响应 JSON: %s" % raw_json)


if __name__ == "__main__":
    main()
