// Package db 负责 SQLite 连接、迁移与全部数据访问。
package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // 纯 Go SQLite 驱动，注册 driver 名 "sqlite"

	"kongtan/internal/model"
)

// TimeLayout 统一的时间字符串格式。
const TimeLayout = "2006-01-02 15:04:05"

// Now 返回统一格式的当前时间字符串。
func Now() string { return time.Now().Format(TimeLayout) }

// Store SQLite 数据访问层。
type Store struct {
	DB *sql.DB
}

// Open 打开（必要时创建）SQLite 数据库并执行迁移。
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("创建数据库目录失败: %w", err)
		}
	}

	dsn := path
	if !strings.HasPrefix(path, ":memory:") {
		dsn = "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(off)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	// SQLite 单写者，限制连接数避免锁竞争。
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("连接数据库失败: %w", err)
	}

	s := &Store{DB: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.DB.Close() }

// migrate 执行建表与索引迁移。
func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS groups (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			description TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS api_keys (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			token TEXT NOT NULL,
			sort_order INTEGER NOT NULL DEFAULT 0,
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS domains (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			domain TEXT NOT NULL,
			group_id INTEGER NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL,
			UNIQUE(group_id, domain)
		)`,
		`CREATE TABLE IF NOT EXISTS blacklist (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			type TEXT NOT NULL,
			value TEXT NOT NULL,
			created_at TEXT NOT NULL,
			UNIQUE(type, value)
		)`,
		`CREATE TABLE IF NOT EXISTS tasks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			group_id INTEGER NOT NULL DEFAULT 0,
			group_name TEXT NOT NULL DEFAULT '',
			task_name TEXT NOT NULL DEFAULT '',
			trigger_type TEXT NOT NULL,
			status TEXT NOT NULL,
			started_at TEXT NOT NULL,
			finished_at TEXT NOT NULL DEFAULT '',
			page_size INTEGER NOT NULL DEFAULT 0,
			total_records INTEGER NOT NULL DEFAULT 0,
			consumed_points INTEGER NOT NULL DEFAULT 0,
			error_msg TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS urls (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id INTEGER NOT NULL,
			task_name TEXT NOT NULL DEFAULT '',
			group_name TEXT NOT NULL DEFAULT '',
			url TEXT NOT NULL,
			host TEXT NOT NULL DEFAULT '',
			ip TEXT NOT NULL DEFAULT '',
			port INTEGER NOT NULL DEFAULT 0,
			title TEXT NOT NULL DEFAULT '',
			domain TEXT NOT NULL DEFAULT '',
			icp_licence TEXT NOT NULL DEFAULT '',
			icp_unit TEXT NOT NULL DEFAULT '',
			icp_nature TEXT NOT NULL DEFAULT '',
			icp_expired TEXT NOT NULL DEFAULT '',
			province_cn TEXT NOT NULL DEFAULT '',
			city_cn TEXT NOT NULL DEFAULT '',
			country_cn TEXT NOT NULL DEFAULT '',
			isp TEXT NOT NULL DEFAULT '',
			asname TEXT NOT NULL DEFAULT '',
			scene_cn TEXT NOT NULL DEFAULT '',
			gps_lat TEXT NOT NULL DEFAULT '',
			gps_lng TEXT NOT NULL DEFAULT '',
			raw_json TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_urls_task ON urls(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_urls_domain ON urls(domain)`,
		`CREATE INDEX IF NOT EXISTS idx_urls_group ON urls(group_name)`,
		`CREATE TABLE IF NOT EXISTS subdomains (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id INTEGER NOT NULL,
			task_name TEXT NOT NULL DEFAULT '',
			group_name TEXT NOT NULL DEFAULT '',
			subdomain TEXT NOT NULL,
			domain TEXT NOT NULL DEFAULT '',
			host TEXT NOT NULL DEFAULT '',
			icp_licence TEXT NOT NULL DEFAULT '',
			icp_unit TEXT NOT NULL DEFAULT '',
			icp_nature TEXT NOT NULL DEFAULT '',
			icp_expired TEXT NOT NULL DEFAULT '',
			province_cn TEXT NOT NULL DEFAULT '',
			city_cn TEXT NOT NULL DEFAULT '',
			country_cn TEXT NOT NULL DEFAULT '',
			isp TEXT NOT NULL DEFAULT '',
			asname TEXT NOT NULL DEFAULT '',
			scene_cn TEXT NOT NULL DEFAULT '',
			gps_lat TEXT NOT NULL DEFAULT '',
			gps_lng TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_subdomains_task ON subdomains(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_subdomains_domain ON subdomains(domain)`,
		`CREATE INDEX IF NOT EXISTS idx_subdomains_group ON subdomains(group_name)`,
		`CREATE TABLE IF NOT EXISTS schedule_runs (
			month_key TEXT PRIMARY KEY,
			executed_at TEXT NOT NULL
		)`,
	}

	for _, q := range stmts {
		if _, err := s.DB.Exec(q); err != nil {
			return fmt.Errorf("迁移失败: %w\nSQL: %s", err, q)
		}
	}
	return nil
}

// ---------- 分组 ----------

// CreateGroup 新建分组，返回新 ID。
func (s *Store) CreateGroup(name, desc string) (int64, error) {
	res, err := s.DB.Exec(`INSERT INTO groups(name, description, created_at) VALUES(?,?,?)`, name, desc, Now())
	if err != nil {
		return 0, fmt.Errorf("创建分组失败: %w", err)
	}
	return res.LastInsertId()
}

// UpdateGroup 更新分组。
func (s *Store) UpdateGroup(id int64, name, desc string) error {
	_, err := s.DB.Exec(`UPDATE groups SET name=?, description=? WHERE id=?`, name, desc, id)
	if err != nil {
		return fmt.Errorf("更新分组失败: %w", err)
	}
	return nil
}

// DeleteGroup 删除分组及其下域名。
func (s *Store) DeleteGroup(id int64) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM domains WHERE group_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM groups WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// ListGroups 列出全部分组（按创建顺序）。
func (s *Store) ListGroups() ([]model.Group, error) {
	rows, err := s.DB.Query(`SELECT id, name, description, created_at FROM groups ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Group
	for rows.Next() {
		var g model.Group
		if err := rows.Scan(&g.ID, &g.Name, &g.Description, &g.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	if out == nil {
		out = []model.Group{}
	}
	return out, rows.Err()
}

// GetGroup 按 ID 取分组。
func (s *Store) GetGroup(id int64) (*model.Group, error) {
	var g model.Group
	err := s.DB.QueryRow(`SELECT id, name, description, created_at FROM groups WHERE id=?`, id).
		Scan(&g.ID, &g.Name, &g.Description, &g.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &g, nil
}

// GetGroupByName 按名称取分组。
func (s *Store) GetGroupByName(name string) (*model.Group, error) {
	var g model.Group
	err := s.DB.QueryRow(`SELECT id, name, description, created_at FROM groups WHERE name=?`, name).
		Scan(&g.ID, &g.Name, &g.Description, &g.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &g, nil
}

// CountGroups 分组总数。
func (s *Store) CountGroups() (int, error) {
	return s.count("groups")
}

// ---------- API Token ----------

// CreateAPIKey 新增 API Token。
func (s *Store) CreateAPIKey(name, token string, sortOrder int, enabled bool) (int64, error) {
	res, err := s.DB.Exec(`INSERT INTO api_keys(name, token, sort_order, enabled, created_at) VALUES(?,?,?,?,?)`,
		name, token, sortOrder, boolInt(enabled), Now())
	if err != nil {
		return 0, fmt.Errorf("新增 API 失败: %w", err)
	}
	return res.LastInsertId()
}

// UpdateAPIKey 更新 API Token。
func (s *Store) UpdateAPIKey(id int64, name, token string, sortOrder int, enabled bool) error {
	_, err := s.DB.Exec(`UPDATE api_keys SET name=?, token=?, sort_order=?, enabled=? WHERE id=?`,
		name, token, sortOrder, boolInt(enabled), id)
	if err != nil {
		return fmt.Errorf("更新 API 失败: %w", err)
	}
	return nil
}

// DeleteAPIKey 删除 API Token。
func (s *Store) DeleteAPIKey(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM api_keys WHERE id=?`, id)
	return err
}

// ListAPIKeys 按 sort_order、id 列出全部 API。
func (s *Store) ListAPIKeys() ([]model.APIKey, error) {
	rows, err := s.DB.Query(`SELECT id, name, token, sort_order, enabled, created_at FROM api_keys ORDER BY sort_order, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.APIKey
	for rows.Next() {
		var k model.APIKey
		var en int
		if err := rows.Scan(&k.ID, &k.Name, &k.Token, &k.SortOrder, &en, &k.CreatedAt); err != nil {
			return nil, err
		}
		k.Enabled = en != 0
		out = append(out, k)
	}
	if out == nil {
		out = []model.APIKey{}
	}
	return out, rows.Err()
}

// GetAPIKey 取单个 API。
func (s *Store) GetAPIKey(id int64) (*model.APIKey, error) {
	var k model.APIKey
	var en int
	err := s.DB.QueryRow(`SELECT id, name, token, sort_order, enabled, created_at FROM api_keys WHERE id=?`, id).
		Scan(&k.ID, &k.Name, &k.Token, &k.SortOrder, &en, &k.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	k.Enabled = en != 0
	return &k, nil
}

// GetEnabledAPIKeys 取启用且按 sort_order 排序的 API。
func (s *Store) GetEnabledAPIKeys() ([]model.APIKey, error) {
	rows, err := s.DB.Query(`SELECT id, name, token, sort_order, enabled, created_at FROM api_keys WHERE enabled=1 ORDER BY sort_order, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.APIKey
	for rows.Next() {
		var k model.APIKey
		var en int
		if err := rows.Scan(&k.ID, &k.Name, &k.Token, &k.SortOrder, &en, &k.CreatedAt); err != nil {
			return nil, err
		}
		k.Enabled = en != 0
		out = append(out, k)
	}
	if out == nil {
		out = []model.APIKey{}
	}
	return out, rows.Err()
}

// ReorderAPIKeys 按给定 ID 顺序重写 sort_order。
func (s *Store) ReorderAPIKeys(ids []int64) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, id := range ids {
		if _, err := tx.Exec(`UPDATE api_keys SET sort_order=? WHERE id=?`, i, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// CountAPIKeys API 总数。
func (s *Store) CountAPIKeys() (int, error) { return s.count("api_keys") }

// CountEnabledAPIKeys 启用的 API 数。
func (s *Store) CountEnabledAPIKeys() (int, error) {
	return s.countWhere("api_keys", "enabled=1")
}

// ---------- 域名 ----------

// AddDomains 批量新增域名（同分组内按 (group_id, domain) 去重，重复忽略）。
func (s *Store) AddDomains(groupID int64, domains []string) (int, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	added := 0
	now := Now()
	for _, d := range domains {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		res, err := tx.Exec(`INSERT OR IGNORE INTO domains(domain, group_id, enabled, created_at) VALUES(?,?,1,?)`, d, groupID, now)
		if err != nil {
			return 0, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			added++
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return added, nil
}

// ListDomains 列出某分组下的域名（按录入顺序）。
func (s *Store) ListDomains(groupID int64) ([]model.Domain, error) {
	rows, err := s.DB.Query(`SELECT id, domain, group_id, enabled, created_at FROM domains WHERE group_id=? ORDER BY id`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Domain
	for rows.Next() {
		var d model.Domain
		var en int
		if err := rows.Scan(&d.ID, &d.Domain, &d.GroupID, &en, &d.CreatedAt); err != nil {
			return nil, err
		}
		d.Enabled = en != 0
		out = append(out, d)
	}
	if out == nil {
		out = []model.Domain{}
	}
	return out, rows.Err()
}

// ListAllEnabledDomains 列出所有启用分组下的启用域名（含分组名，按录入顺序）。
func (s *Store) ListAllEnabledDomains() ([]model.Domain, error) {
	rows, err := s.DB.Query(`SELECT d.id, d.domain, d.group_id, g.name, d.enabled, d.created_at
		FROM domains d JOIN groups g ON g.id = d.group_id
		WHERE d.enabled=1 ORDER BY d.group_id, d.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Domain
	for rows.Next() {
		var d model.Domain
		var en int
		if err := rows.Scan(&d.ID, &d.Domain, &d.GroupID, &d.GroupName, &en, &d.CreatedAt); err != nil {
			return nil, err
		}
		d.Enabled = en != 0
		out = append(out, d)
	}
	if out == nil {
		out = []model.Domain{}
	}
	return out, rows.Err()
}

// UpdateDomain 更新域名。
func (s *Store) UpdateDomain(id int64, domain string, enabled bool) error {
	_, err := s.DB.Exec(`UPDATE domains SET domain=?, enabled=? WHERE id=?`, strings.TrimSpace(domain), boolInt(enabled), id)
	return err
}

// DeleteDomain 删除域名。
func (s *Store) DeleteDomain(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM domains WHERE id=?`, id)
	return err
}

// CountDomains 域名总数。
func (s *Store) CountDomains() (int, error) { return s.count("domains") }

// CountDomainsByGroup 某分组域名数。
func (s *Store) CountDomainsByGroup(groupID int64) (int, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM domains WHERE group_id=?`, groupID).Scan(&n)
	return n, err
}

// ---------- 黑名单 ----------

// ListBlacklist 列出全部黑名单条目。
func (s *Store) ListBlacklist() ([]model.BlacklistEntry, error) {
	rows, err := s.DB.Query(`SELECT id, type, value, created_at FROM blacklist ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.BlacklistEntry
	for rows.Next() {
		var b model.BlacklistEntry
		if err := rows.Scan(&b.ID, &b.Type, &b.Value, &b.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	if out == nil {
		out = []model.BlacklistEntry{}
	}
	return out, rows.Err()
}

// AddBlacklist 新增黑名单（type ∈ keyword/regex，重复忽略）。
func (s *Store) AddBlacklist(typ, value string) (int64, error) {
	value = strings.TrimSpace(value)
	res, err := s.DB.Exec(`INSERT OR IGNORE INTO blacklist(type, value, created_at) VALUES(?,?,?)`, typ, value, Now())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// DeleteBlacklist 删除黑名单。
func (s *Store) DeleteBlacklist(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM blacklist WHERE id=?`, id)
	return err
}

// CountBlacklist 黑名单条数。
func (s *Store) CountBlacklist() (int, error) { return s.count("blacklist") }

// ---------- 任务 ----------

// CreateTask 创建任务，返回新 ID。
func (s *Store) CreateTask(t *model.Task) (int64, error) {
	res, err := s.DB.Exec(`INSERT INTO tasks(group_id, group_name, task_name, trigger_type, status, started_at, finished_at, page_size, total_records, consumed_points, error_msg)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		t.GroupID, t.GroupName, t.TaskName, t.TriggerType, t.Status, t.StartedAt, t.FinishedAt, t.PageSize, t.TotalRecords, t.ConsumedPoints, t.ErrorMsg)
	if err != nil {
		return 0, fmt.Errorf("创建任务失败: %w", err)
	}
	return res.LastInsertId()
}

// FinalizeTask 结束任务并写入汇总信息。
func (s *Store) FinalizeTask(id int64, status string, totalRecords, consumedPoints int, errorMsg string) error {
	_, err := s.DB.Exec(`UPDATE tasks SET status=?, finished_at=?, total_records=?, consumed_points=?, error_msg=? WHERE id=?`,
		status, Now(), totalRecords, consumedPoints, errorMsg, id)
	return err
}

// GetTask 取任务。
func (s *Store) GetTask(id int64) (*model.Task, error) {
	t := &model.Task{}
	err := s.DB.QueryRow(`SELECT id, group_id, group_name, task_name, trigger_type, status, started_at, finished_at, page_size, total_records, consumed_points, error_msg
		FROM tasks WHERE id=?`, id).
		Scan(&t.ID, &t.GroupID, &t.GroupName, &t.TaskName, &t.TriggerType, &t.Status, &t.StartedAt, &t.FinishedAt, &t.PageSize, &t.TotalRecords, &t.ConsumedPoints, &t.ErrorMsg)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return t, nil
}

// ListTasks 分页列出任务（倒序）。
func (s *Store) ListTasks(limit, offset int) ([]model.Task, error) {
	rows, err := s.DB.Query(`SELECT id, group_id, group_name, task_name, trigger_type, status, started_at, finished_at, page_size, total_records, consumed_points, error_msg
		FROM tasks ORDER BY id DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Task
	for rows.Next() {
		var t model.Task
		if err := rows.Scan(&t.ID, &t.GroupID, &t.GroupName, &t.TaskName, &t.TriggerType, &t.Status, &t.StartedAt, &t.FinishedAt, &t.PageSize, &t.TotalRecords, &t.ConsumedPoints, &t.ErrorMsg); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if out == nil {
		out = []model.Task{}
	}
	return out, rows.Err()
}

// CountTasks 任务总数。
func (s *Store) CountTasks() (int, error) { return s.count("tasks") }

// TaskStatusCounts 各状态任务数。
func (s *Store) TaskStatusCounts() (running, success, failed, partial int, err error) {
	rows, err := s.DB.Query(`SELECT status, COUNT(*) FROM tasks GROUP BY status`)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return 0, 0, 0, 0, err
		}
		switch st {
		case model.StatusRunning:
			running = n
		case model.StatusSuccess:
			success = n
		case model.StatusFailed:
			failed = n
		case model.StatusPartial:
			partial = n
		}
	}
	return running, success, failed, partial, rows.Err()
}

// LastTaskAt 最近一次任务的开始时间。
func (s *Store) LastTaskAt() (string, error) {
	var t string
	err := s.DB.QueryRow(`SELECT started_at FROM tasks ORDER BY id DESC LIMIT 1`).Scan(&t)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return t, err
}

// MonthPoints 某月（year/month）任务累计消耗点数。
func (s *Store) MonthPoints(year, month int) (int, error) {
	prefix := fmt.Sprintf("%04d-%02d", year, month)
	var n int
	err := s.DB.QueryRow(`SELECT COALESCE(SUM(consumed_points),0) FROM tasks WHERE started_at LIKE ?`, prefix+"%").Scan(&n)
	return n, err
}

// ---------- URL ----------

// InsertURLs 批量插入 URL 记录。
func (s *Store) InsertURLs(recs []model.URLRecord) error {
	if len(recs) == 0 {
		return nil
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT INTO urls(task_id, task_name, group_name, url, host, ip, port, title, domain,
		icp_licence, icp_unit, icp_nature, icp_expired,
		province_cn, city_cn, country_cn, isp, asname, scene_cn, gps_lat, gps_lng, raw_json, created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, r := range recs {
		if _, err := stmt.Exec(r.TaskID, r.TaskName, r.GroupName, r.URL, r.Host, r.IP, r.Port, r.Title, r.Domain,
			r.ICPLicence, r.ICPUnit, r.ICPNature, r.ICPExpired,
			r.ProvinceCN, r.CityCN, r.CountryCN, r.ISP, r.ASName, r.SceneCN, r.GPSLat, r.GPSLng, r.RawJSON, r.CreatedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// URLFilter URL 查询过滤条件。
type URLFilter struct {
	GroupName string
	TaskID    int64
	Domain    string
	Host      string
	ICP       string
	Query     string
	Limit     int
	Offset    int
	Dedup     bool // 以 url 为索引去重，保留最新记录
}

const urlCols = `u.id, u.task_id, u.task_name, u.group_name, u.url, u.host, u.ip, u.port, u.title, u.domain,
	u.icp_licence, u.icp_unit, u.icp_nature, u.icp_expired,
	u.province_cn, u.city_cn, u.country_cn, u.isp, u.asname, u.scene_cn, u.gps_lat, u.gps_lng, u.raw_json, u.created_at,
	COALESCE(t.started_at, '')`

// ListURLs 按过滤条件查询 URL；Dedup 时以 url 为索引去重保留最新。
func (s *Store) ListURLs(f URLFilter) ([]model.URLRecord, error) {
	where, args := buildURLWhere(f)
	var q string
	if f.Dedup {
		q = `SELECT ` + urlCols + ` FROM urls u LEFT JOIN tasks t ON t.id = u.task_id
			WHERE u.id IN (SELECT MAX(u.id) FROM urls u` + where + ` GROUP BY u.url)
			ORDER BY u.id DESC LIMIT ? OFFSET ?`
	} else {
		q = `SELECT ` + urlCols + ` FROM urls u LEFT JOIN tasks t ON t.id = u.task_id
			` + where + ` ORDER BY u.id DESC LIMIT ? OFFSET ?`
	}
	args = append(args, f.Limit, f.Offset)
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.URLRecord
	for rows.Next() {
		var r model.URLRecord
		if err := rows.Scan(&r.ID, &r.TaskID, &r.TaskName, &r.GroupName, &r.URL, &r.Host, &r.IP, &r.Port, &r.Title, &r.Domain,
			&r.ICPLicence, &r.ICPUnit, &r.ICPNature, &r.ICPExpired,
			&r.ProvinceCN, &r.CityCN, &r.CountryCN, &r.ISP, &r.ASName, &r.SceneCN, &r.GPSLat, &r.GPSLng, &r.RawJSON, &r.CreatedAt,
			&r.TaskStartedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if out == nil {
		out = []model.URLRecord{}
	}
	return out, rows.Err()
}

// CountURLs 统计过滤条件下的 URL 数（Dedup 时按 url 去重计数）。
func (s *Store) CountURLs(f URLFilter) (int, error) {
	where, args := buildURLWhere(f)
	var q string
	if f.Dedup {
		q = `SELECT COUNT(*) FROM (SELECT u.id FROM urls u` + where + ` GROUP BY u.url)`
	} else {
		q = `SELECT COUNT(*) FROM urls u` + where
	}
	var n int
	err := s.DB.QueryRow(q, args...).Scan(&n)
	return n, err
}

// URLsByTask 取某任务全部 URL（用于导出，不去重）。
func (s *Store) URLsByTask(taskID int64) ([]model.URLRecord, error) {
	return s.ListURLs(URLFilter{TaskID: taskID, Limit: 1 << 30, Offset: 0})
}

func buildURLWhere(f URLFilter) (string, []any) {
	var conds []string
	var args []any
	if f.GroupName != "" {
		conds = append(conds, "u.group_name = ?")
		args = append(args, f.GroupName)
	}
	if f.TaskID > 0 {
		conds = append(conds, "u.task_id = ?")
		args = append(args, f.TaskID)
	}
	if f.Domain != "" {
		conds = append(conds, "u.domain LIKE ?")
		args = append(args, "%"+f.Domain+"%")
	}
	if f.Host != "" {
		conds = append(conds, "u.host LIKE ?")
		args = append(args, "%"+f.Host+"%")
	}
	if f.ICP != "" {
		conds = append(conds, "(u.icp_licence LIKE ? OR u.icp_unit LIKE ?)")
		args = append(args, "%"+f.ICP+"%", "%"+f.ICP+"%")
	}
	if f.Query != "" {
		conds = append(conds, "(u.url LIKE ? OR u.host LIKE ? OR u.domain LIKE ? OR u.title LIKE ?)")
		args = append(args, "%"+f.Query+"%", "%"+f.Query+"%", "%"+f.Query+"%", "%"+f.Query+"%")
	}
	if len(conds) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

// ---------- 子域名 ----------

// InsertSubdomains 批量插入子域名记录。
func (s *Store) InsertSubdomains(recs []model.SubdomainRecord) error {
	if len(recs) == 0 {
		return nil
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT INTO subdomains(task_id, task_name, group_name, subdomain, domain, host,
		icp_licence, icp_unit, icp_nature, icp_expired,
		province_cn, city_cn, country_cn, isp, asname, scene_cn, gps_lat, gps_lng, created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, r := range recs {
		if _, err := stmt.Exec(r.TaskID, r.TaskName, r.GroupName, r.Subdomain, r.Domain, r.Host,
			r.ICPLicence, r.ICPUnit, r.ICPNature, r.ICPExpired,
			r.ProvinceCN, r.CityCN, r.CountryCN, r.ISP, r.ASName, r.SceneCN, r.GPSLat, r.GPSLng, r.CreatedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SubdomainFilter 子域名查询过滤条件。
type SubdomainFilter struct {
	GroupName string
	TaskID    int64
	Domain    string
	Host      string
	ICP       string
	Query     string
	Limit     int
	Offset    int
	Dedup     bool // 以 subdomain 为索引去重，保留最新记录
}

const subCols = `s.id, s.task_id, s.task_name, s.group_name, s.subdomain, s.domain, s.host,
	s.icp_licence, s.icp_unit, s.icp_nature, s.icp_expired,
	s.province_cn, s.city_cn, s.country_cn, s.isp, s.asname, s.scene_cn, s.gps_lat, s.gps_lng, s.created_at,
	COALESCE(t.started_at, '')`

// ListSubdomains 按过滤条件查询子域名；Dedup 时以 subdomain 为索引去重保留最新。
func (s *Store) ListSubdomains(f SubdomainFilter) ([]model.SubdomainRecord, error) {
	where, args := buildSubWhere(f)
	var q string
	if f.Dedup {
		q = `SELECT ` + subCols + ` FROM subdomains s LEFT JOIN tasks t ON t.id = s.task_id
			WHERE s.id IN (SELECT MAX(s.id) FROM subdomains s` + where + ` GROUP BY s.subdomain)
			ORDER BY s.id DESC LIMIT ? OFFSET ?`
	} else {
		q = `SELECT ` + subCols + ` FROM subdomains s LEFT JOIN tasks t ON t.id = s.task_id
			` + where + ` ORDER BY s.id DESC LIMIT ? OFFSET ?`
	}
	args = append(args, f.Limit, f.Offset)
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.SubdomainRecord
	for rows.Next() {
		var r model.SubdomainRecord
		if err := rows.Scan(&r.ID, &r.TaskID, &r.TaskName, &r.GroupName, &r.Subdomain, &r.Domain, &r.Host,
			&r.ICPLicence, &r.ICPUnit, &r.ICPNature, &r.ICPExpired,
			&r.ProvinceCN, &r.CityCN, &r.CountryCN, &r.ISP, &r.ASName, &r.SceneCN, &r.GPSLat, &r.GPSLng, &r.CreatedAt,
			&r.TaskStartedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if out == nil {
		out = []model.SubdomainRecord{}
	}
	return out, rows.Err()
}

// CountSubdomains 统计过滤条件下的子域名数（Dedup 时按 subdomain 去重计数）。
func (s *Store) CountSubdomains(f SubdomainFilter) (int, error) {
	where, args := buildSubWhere(f)
	var q string
	if f.Dedup {
		q = `SELECT COUNT(*) FROM (SELECT s.id FROM subdomains s` + where + ` GROUP BY s.subdomain)`
	} else {
		q = `SELECT COUNT(*) FROM subdomains s` + where
	}
	var n int
	err := s.DB.QueryRow(q, args...).Scan(&n)
	return n, err
}

// SubdomainsByTask 取某任务全部子域名（用于导出，不去重）。
func (s *Store) SubdomainsByTask(taskID int64) ([]model.SubdomainRecord, error) {
	return s.ListSubdomains(SubdomainFilter{TaskID: taskID, Limit: 1 << 30, Offset: 0})
}

func buildSubWhere(f SubdomainFilter) (string, []any) {
	var conds []string
	var args []any
	if f.GroupName != "" {
		conds = append(conds, "s.group_name = ?")
		args = append(args, f.GroupName)
	}
	if f.TaskID > 0 {
		conds = append(conds, "s.task_id = ?")
		args = append(args, f.TaskID)
	}
	if f.Domain != "" {
		conds = append(conds, "s.domain LIKE ?")
		args = append(args, "%"+f.Domain+"%")
	}
	if f.Host != "" {
		conds = append(conds, "s.host LIKE ?")
		args = append(args, "%"+f.Host+"%")
	}
	if f.ICP != "" {
		conds = append(conds, "(s.icp_licence LIKE ? OR s.icp_unit LIKE ?)")
		args = append(args, "%"+f.ICP+"%", "%"+f.ICP+"%")
	}
	if f.Query != "" {
		conds = append(conds, "(s.subdomain LIKE ? OR s.host LIKE ? OR s.domain LIKE ?)")
		args = append(args, "%"+f.Query+"%", "%"+f.Query+"%", "%"+f.Query+"%")
	}
	if len(conds) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

// ---------- 调度标记 ----------

// HasScheduleRun 本月是否已执行过定时任务。
func (s *Store) HasScheduleRun(monthKey string) (bool, error) {
	var one int
	err := s.DB.QueryRow(`SELECT 1 FROM schedule_runs WHERE month_key=?`, monthKey).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// MarkScheduleRun 记录本月定时任务已执行。
func (s *Store) MarkScheduleRun(monthKey string) error {
	_, err := s.DB.Exec(`INSERT OR REPLACE INTO schedule_runs(month_key, executed_at) VALUES(?,?)`, monthKey, Now())
	return err
}

// GetScheduleRun 取某月的调度执行标记时间；未执行返回空字符串。
func (s *Store) GetScheduleRun(monthKey string) (string, error) {
	var t string
	err := s.DB.QueryRow(`SELECT executed_at FROM schedule_runs WHERE month_key=?`, monthKey).Scan(&t)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return t, nil
}

// ListScheduledTasks 按时间倒序列出定时触发的任务。
func (s *Store) ListScheduledTasks(limit int) ([]model.Task, error) {
	rows, err := s.DB.Query(`SELECT id, group_id, group_name, task_name, trigger_type, status, started_at, finished_at, page_size, total_records, consumed_points, error_msg
		FROM tasks WHERE trigger_type=? ORDER BY id DESC LIMIT ?`, model.TriggerScheduled, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Task
	for rows.Next() {
		var t model.Task
		if err := rows.Scan(&t.ID, &t.GroupID, &t.GroupName, &t.TaskName, &t.TriggerType, &t.Status, &t.StartedAt, &t.FinishedAt, &t.PageSize, &t.TotalRecords, &t.ConsumedPoints, &t.ErrorMsg); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if out == nil {
		out = []model.Task{}
	}
	return out, rows.Err()
}

// FirstScheduledTaskInMonth 取某自然月内最早的一条定时任务（无则返回 nil）。
func (s *Store) FirstScheduledTaskInMonth(year, month int) (*model.Task, error) {
	prefix := fmt.Sprintf("%04d-%02d", year, month)
	t := &model.Task{}
	err := s.DB.QueryRow(`SELECT id, group_id, group_name, task_name, trigger_type, status, started_at, finished_at, page_size, total_records, consumed_points, error_msg
		FROM tasks WHERE trigger_type=? AND started_at LIKE ? ORDER BY id ASC LIMIT 1`,
		model.TriggerScheduled, prefix+"%").
		Scan(&t.ID, &t.GroupID, &t.GroupName, &t.TaskName, &t.TriggerType, &t.Status, &t.StartedAt, &t.FinishedAt, &t.PageSize, &t.TotalRecords, &t.ConsumedPoints, &t.ErrorMsg)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return t, nil
}

// ---------- 内部工具 ----------

func (s *Store) count(table string) (int, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n)
	return n, err
}

func (s *Store) countWhere(table, where string) (int, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM ` + table + ` WHERE ` + where).Scan(&n)
	return n, err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
