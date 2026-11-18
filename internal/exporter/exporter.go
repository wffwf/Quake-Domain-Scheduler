// Package exporter 负责 CSV 导出（UTF-8 with BOM，兼容 Excel 中文）。
package exporter

import (
	"encoding/csv"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"kongtan/internal/model"
)

// URLHeader URL 导出字段顺序（与需求 §6.7 一致）。
var URLHeader = []string{
	"task_name", "group_name", "url", "host", "ip", "port", "title", "domain",
	"icp_licence", "icp_unit", "icp_nature", "icp_expired",
	"province_cn", "city_cn", "country_cn", "isp", "asname", "scene_cn", "gps_lat", "gps_lng",
}

// SubdomainHeader 子域名导出字段顺序。
var SubdomainHeader = []string{
	"task_name", "group_name", "subdomain", "domain", "host",
	"icp_licence", "icp_unit", "icp_nature", "icp_expired",
	"province_cn", "city_cn", "country_cn", "isp", "asname", "scene_cn", "gps_lat", "gps_lng",
}

// WriteURLs 导出 URL CSV 到文件。
func WriteURLs(path string, recs []model.URLRecord) error {
	return writeFile(path, func(w io.Writer) error { return WriteURLsTo(w, recs) })
}

// WriteURLsTo 将 URL CSV（含 BOM）写入任意 io.Writer。
func WriteURLsTo(w io.Writer, recs []model.URLRecord) error {
	rows := make([][]string, 0, len(recs))
	for _, r := range recs {
		rows = append(rows, []string{
			r.TaskName, r.GroupName, r.URL, r.Host, r.IP, strconv.Itoa(r.Port), r.Title, r.Domain,
			r.ICPLicence, r.ICPUnit, r.ICPNature, r.ICPExpired,
			r.ProvinceCN, r.CityCN, r.CountryCN, r.ISP, r.ASName, r.SceneCN, r.GPSLat, r.GPSLng,
		})
	}
	return writeCSVTo(w, URLHeader, rows)
}

// WriteSubdomains 导出子域名 CSV 到文件。
func WriteSubdomains(path string, recs []model.SubdomainRecord) error {
	return writeFile(path, func(w io.Writer) error { return WriteSubdomainsTo(w, recs) })
}

// WriteSubdomainsTo 将子域名 CSV（含 BOM）写入任意 io.Writer。
func WriteSubdomainsTo(w io.Writer, recs []model.SubdomainRecord) error {
	rows := make([][]string, 0, len(recs))
	for _, r := range recs {
		rows = append(rows, []string{
			r.TaskName, r.GroupName, r.Subdomain, r.Domain, r.Host,
			r.ICPLicence, r.ICPUnit, r.ICPNature, r.ICPExpired,
			r.ProvinceCN, r.CityCN, r.CountryCN, r.ISP, r.ASName, r.SceneCN, r.GPSLat, r.GPSLng,
		})
	}
	return writeCSVTo(w, SubdomainHeader, rows)
}

func writeFile(path string, fn func(io.Writer) error) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return fn(f)
}

func writeCSVTo(w io.Writer, header []string, rows [][]string) error {
	// UTF-8 BOM。
	if _, err := w.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	if err := cw.Write(header); err != nil {
		return err
	}
	for _, r := range rows {
		if err := cw.Write(r); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}
