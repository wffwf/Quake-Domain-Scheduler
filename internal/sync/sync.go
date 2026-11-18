// Package sync 负责 API Token 与域名的 txt 文件同步与启动导入。
package sync

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"kongtan/internal/config"
	"kongtan/internal/db"
	"kongtan/internal/model"
)

// SyncAPIKeys 将数据库 API Token 落地为 inputDir/apis.txt（每行 name,token,enabled,sort_order）。
func SyncAPIKeys(s *db.Store, inputDir string) error {
	keys, err := s.ListAPIKeys()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(inputDir, 0o755); err != nil {
		return err
	}
	f, err := os.Create(filepath.Join(inputDir, "apis.txt"))
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	for _, k := range keys {
		en := "0"
		if k.Enabled {
			en = "1"
		}
		if err := w.Write([]string{k.Name, k.Token, en, strconv.Itoa(k.SortOrder)}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

// SyncDomains 将数据库域名落地为 inputDir/domains/<group>.txt（每行一个域名），
// 并清理已不存在分组的孤儿 txt 文件。
func SyncDomains(s *db.Store, inputDir string) error {
	groups, err := s.ListGroups()
	if err != nil {
		return err
	}
	dir := filepath.Join(inputDir, "domains")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	valid := map[string]bool{}
	for _, g := range groups {
		domains, err := s.ListDomains(g.ID)
		if err != nil {
			return err
		}
		safe := SafeName(g.Name)
		valid[safe+".txt"] = true
		path := filepath.Join(dir, safe+".txt")
		f, err := os.Create(path)
		if err != nil {
			return err
		}
		w := bufio.NewWriter(f)
		for _, d := range domains {
			fmt.Fprintln(w, d.Domain)
		}
		w.Flush()
		f.Close()
	}

	// 清理孤儿文件。
	if entries, err := os.ReadDir(dir); err == nil {
		for _, ent := range entries {
			if !ent.IsDir() && strings.HasSuffix(ent.Name(), ".txt") && !valid[ent.Name()] {
				_ = os.Remove(filepath.Join(dir, ent.Name()))
			}
		}
	}
	return nil
}

// ImportAPIKeys 启动导入：仅当数据库 api_keys 为空且 apis.txt 存在时导入。
func ImportAPIKeys(s *db.Store, inputDir string) (int, error) {
	n, err := s.CountAPIKeys()
	if err != nil {
		return 0, err
	}
	if n > 0 {
		return 0, nil
	}
	path := filepath.Join(inputDir, "apis.txt")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	defer f.Close()

	imported := 0
	r := csv.NewReader(f)
	r.FieldsPerRecord = -1 // 允许字段数不一致
	order := 0
	for {
		rec, err := r.Read()
		if err != nil {
			break // io.EOF 或格式错误
		}
		if len(rec) == 0 || strings.TrimSpace(rec[0]) == "" || strings.HasPrefix(strings.TrimSpace(rec[0]), "#") {
			continue
		}
		name := strings.TrimSpace(rec[0])
		token := ""
		if len(rec) > 1 {
			token = strings.TrimSpace(rec[1])
		}
		enabled := true
		if len(rec) > 2 {
			enabled = strings.TrimSpace(rec[2]) != "0"
		}
		sortOrder := order
		if len(rec) > 3 {
			if v, e := strconv.Atoi(strings.TrimSpace(rec[3])); e == nil {
				sortOrder = v
			}
		}
		if token == "" {
			continue
		}
		if _, err := s.CreateAPIKey(name, token, sortOrder, enabled); err != nil {
			return imported, err
		}
		imported++
		order++
	}
	return imported, nil
}

// ImportDomains 启动导入：对每个 domains/<group>.txt，若该分组下无域名则导入。
func ImportDomains(s *db.Store, inputDir string) (int, error) {
	dir := filepath.Join(inputDir, "domains")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	imported := 0
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".txt") {
			continue
		}
		groupName := strings.TrimSuffix(ent.Name(), ".txt")
		if groupName == "" {
			continue
		}
		group, err := s.GetGroupByName(groupName)
		if err != nil {
			return imported, err
		}
		if group == nil {
			id, err := s.CreateGroup(groupName, "自动导入")
			if err != nil {
				return imported, err
			}
			group = &model.Group{ID: id, Name: groupName}
		}
		cnt, err := s.CountDomainsByGroup(group.ID)
		if err != nil {
			return imported, err
		}
		if cnt > 0 {
			continue
		}
		domains, err := readDomainLines(filepath.Join(dir, ent.Name()))
		if err != nil {
			return imported, err
		}
		n, err := s.AddDomains(group.ID, domains)
		if err != nil {
			return imported, err
		}
		imported += n
	}
	return imported, nil
}

// ImportConfigAPIs 启动导入：仅当数据库 api_keys 为空时，从 config.yaml 的 quake.apis 导入。
func ImportConfigAPIs(s *db.Store, apis []config.APIConfig) (int, error) {
	n, err := s.CountAPIKeys()
	if err != nil {
		return 0, err
	}
	if n > 0 {
		return 0, nil
	}
	imported := 0
	for i, a := range apis {
		token := strings.TrimSpace(a.Token)
		if token == "" {
			continue
		}
		name := strings.TrimSpace(a.Name)
		if name == "" {
			name = fmt.Sprintf("api-%d", i+1)
		}
		if _, err := s.CreateAPIKey(name, token, i, a.Enabled); err != nil {
			return imported, err
		}
		imported++
	}
	return imported, nil
}

func readDomainLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out, sc.Err()
}

// SafeName 将分组名转换为安全的文件名（替换路径/Windows 非法字符）。
func SafeName(name string) string {
	replacer := strings.NewReplacer(
		"/", "_", "\\", "_", ":", "_", "*", "_", "?", "_",
		"\"", "_", "<", "_", ">", "_", "|", "_", "\n", "_", "\r", "_", "\t", "_",
	)
	name = replacer.Replace(strings.TrimSpace(name))
	name = strings.Trim(name, ". ")
	if name == "" {
		name = "group"
	}
	return name
}
