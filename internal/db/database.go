// Package db 负责 SQLite 数据库的初始化与 DAO（数据访问对象）接口封装。
// 使用纯 Go 实现的 modernc.org/sqlite 驱动，确保 CGO_ENABLED=0 编译通过。
package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"time"

	_ "modernc.org/sqlite"

	"apr-tracker/internal/models"
)

// Database 封装 SQLite 连接与操作
type Database struct {
	conn *sql.DB
	path string
}

// New 创建或打开 SQLite 数据库，自动初始化表结构
func New(dbPath string) (*Database, error) {
	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("打开 SQLite 数据库失败: %w", err)
	}

	// 性能调优
	conn.Exec("PRAGMA journal_mode=WAL")
	conn.Exec("PRAGMA synchronous=NORMAL")
	conn.Exec("PRAGMA cache_size=-8000")

	db := &Database{conn: conn, path: dbPath}
	if err := db.migrate(); err != nil {
		return nil, fmt.Errorf("数据库迁移失败: %w", err)
	}

	return db, nil
}

// migrate 初始化数据库表结构
func (d *Database) migrate() error {
	sqlStmt := `
	CREATE TABLE IF NOT EXISTS versions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		module TEXT NOT NULL,
		version TEXT NOT NULL,
		stages_found TEXT DEFAULT '[]',
		timing TEXT DEFAULT '{}',
		drc TEXT DEFAULT '{}',
		latency TEXT DEFAULT '{}',
		runtime TEXT DEFAULT '{}',
		cellusage TEXT DEFAULT '{}',
		note TEXT DEFAULT '',
		parent_version TEXT DEFAULT '',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(module, version)
	);
	CREATE INDEX IF NOT EXISTS idx_versions_module ON versions(module);
	CREATE INDEX IF NOT EXISTS idx_versions_created ON versions(created_at);
	`
	_, err := d.conn.Exec(sqlStmt)
	return err
}

// Close 关闭数据库连接
func (d *Database) Close() error {
	return d.conn.Close()
}

// Path 返回数据库文件路径
func (d *Database) Path() string {
	return d.path
}

// UpsertVersion 插入或更新一条版本记录（按 module + version 唯一键）
func (d *Database) UpsertVersion(record *models.VersionRecord) error {
	stagesJSON, _ := json.Marshal(record.StagesFound)
	timingJSON, _ := json.Marshal(record.Timing)
	drcJSON, _ := json.Marshal(record.DRC)
	latencyJSON, _ := json.Marshal(record.Latency)
	runtimeJSON, _ := json.Marshal(record.Runtime)
	cellusageJSON, _ := json.Marshal(record.CellUsage)

	now := time.Now().Format(time.RFC3339)

	stmt := `
	INSERT INTO versions (module, version, stages_found, timing, drc, latency, runtime, cellusage, note, parent_version, created_at, updated_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(module, version) DO UPDATE SET
		stages_found = excluded.stages_found,
		timing = excluded.timing,
		drc = excluded.drc,
		latency = excluded.latency,
		runtime = excluded.runtime,
		cellusage = excluded.cellusage,
		note = excluded.note,
		parent_version = excluded.parent_version,
		updated_at = excluded.updated_at
	`

	_, err := d.conn.Exec(stmt,
		record.Module, record.Version,
		string(stagesJSON), string(timingJSON), string(drcJSON),
		string(latencyJSON), string(runtimeJSON), string(cellusageJSON),
		record.Note, record.ParentVersion,
		now, now,
	)
	if err != nil {
		return fmt.Errorf("UpsertVersion 失败 [%s/%s]: %w", record.Module, record.Version, err)
	}
	return nil
}

// GetHistory 获取某模块的所有版本记录，按创建时间降序排列
func (d *Database) GetHistory(module string) ([]*models.VersionRecord, error) {
	rows, err := d.conn.Query(
		`SELECT module, version, stages_found, timing, drc, latency, runtime, cellusage, note, parent_version, created_at, updated_at
		 FROM versions WHERE module = ? ORDER BY created_at DESC`, module)
	if err != nil {
		return nil, fmt.Errorf("查询历史记录失败: %w", err)
	}
	defer rows.Close()

	var records []*models.VersionRecord
	for rows.Next() {
		r := &models.VersionRecord{}
		var stagesStr, timingStr, drcStr, latencyStr, runtimeStr, cellusageStr string
		var createdAt, updatedAt string

		if err := rows.Scan(&r.Module, &r.Version, &stagesStr, &timingStr, &drcStr,
			&latencyStr, &runtimeStr, &cellusageStr, &r.Note, &r.ParentVersion,
			&createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("扫描记录失败: %w", err)
		}

		// 反序列化 JSON 字段
		json.Unmarshal([]byte(stagesStr), &r.StagesFound)
		json.Unmarshal([]byte(timingStr), &r.Timing)
		json.Unmarshal([]byte(drcStr), &r.DRC)
		json.Unmarshal([]byte(latencyStr), &r.Latency)
		json.Unmarshal([]byte(runtimeStr), &r.Runtime)
		json.Unmarshal([]byte(cellusageStr), &r.CellUsage)

		r.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		r.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)

		records = append(records, r)
	}

	return records, nil
}

// GetVersion 获取单个版本记录
func (d *Database) GetVersion(module, version string) (*models.VersionRecord, error) {
	row := d.conn.QueryRow(
		`SELECT module, version, stages_found, timing, drc, latency, runtime, cellusage, note, parent_version, created_at, updated_at
		 FROM versions WHERE module = ? AND version = ?`, module, version)

	r := &models.VersionRecord{}
	var stagesStr, timingStr, drcStr, latencyStr, runtimeStr, cellusageStr string
	var createdAt, updatedAt string

	err := row.Scan(&r.Module, &r.Version, &stagesStr, &timingStr, &drcStr,
		&latencyStr, &runtimeStr, &cellusageStr, &r.Note, &r.ParentVersion,
		&createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询版本失败: %w", err)
	}

	json.Unmarshal([]byte(stagesStr), &r.StagesFound)
	json.Unmarshal([]byte(timingStr), &r.Timing)
	json.Unmarshal([]byte(drcStr), &r.DRC)
	json.Unmarshal([]byte(latencyStr), &r.Latency)
	json.Unmarshal([]byte(runtimeStr), &r.Runtime)
	json.Unmarshal([]byte(cellusageStr), &r.CellUsage)

	r.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	r.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)

	return r, nil
}

// DeleteVersion 删除一条版本记录
func (d *Database) DeleteVersion(module, version string) error {
	result, err := d.conn.Exec("DELETE FROM versions WHERE module = ? AND version = ?", module, version)
	if err != nil {
		return fmt.Errorf("删除版本失败: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("未找到要删除的记录: %s/%s", module, version)
	}
	return nil
}

// UpdateMeta 更新版本的 note 和 parent_version 字段
func (d *Database) UpdateMeta(module, version, note, parentVersion string) error {
	now := time.Now().Format(time.RFC3339)
	result, err := d.conn.Exec(
		`UPDATE versions SET note = ?, parent_version = ?, updated_at = ? WHERE module = ? AND version = ?`,
		note, parentVersion, now, module, version)
	if err != nil {
		return fmt.Errorf("更新元数据失败: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("未找到要更新的记录: %s/%s", module, version)
	}
	return nil
}

// GetAllModules 返回数据库中出现过的所有 module 名
func (d *Database) GetAllModules() ([]string, error) {
	rows, err := d.conn.Query("SELECT DISTINCT module FROM versions ORDER BY module")
	if err != nil {
		return nil, fmt.Errorf("查询模块列表失败: %w", err)
	}
	defer rows.Close()

	var modules []string
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return nil, err
		}
		modules = append(modules, m)
	}
	return modules, nil
}

// Delete 删除数据库文件（用于重置）
func (d *Database) Delete() error {
	d.Close()
	return os.Remove(d.path)
}
