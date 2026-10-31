package main

import (
	"database/sql"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite" // pure-Go SQLite driver (no cgo)

	"github.com/nyk/support-portal/protocol"
)

// Store is the server's durable state: technician logins, the registry of
// client PCs that have ever connected, and an append-only audit log of every
// session. It is safe for concurrent use (database/sql pools connections and
// SQLite serializes writes).
type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS techs (
    username   TEXT PRIMARY KEY,
    pass_hash  TEXT NOT NULL,
    created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS devices (
    id        TEXT PRIMARY KEY,
    hostname  TEXT NOT NULL DEFAULT '',
    username  TEXT NOT NULL DEFAULT '',
    os        TEXT NOT NULL DEFAULT '',
    version   TEXT NOT NULL DEFAULT '',
    nickname  TEXT NOT NULL DEFAULT '',
    last_seen INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS audit_log (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    ts         INTEGER NOT NULL,
    event      TEXT NOT NULL,
    tech       TEXT NOT NULL DEFAULT '',
    device     TEXT NOT NULL DEFAULT '',
    session_id TEXT NOT NULL DEFAULT '',
    detail     TEXT NOT NULL DEFAULT ''
);
`

// OpenStore opens (creating if needed) the SQLite database at path and applies
// the schema.
func OpenStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// SQLite with a single writer: keep one connection to avoid "database is
	// locked" churn, and enable WAL for concurrent readers.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;`); err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// --- technicians ---

// CreateTech adds (or replaces) a technician login with a bcrypt-hashed
// password.
func (s *Store) CreateTech(username, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`INSERT INTO techs(username, pass_hash, created_at) VALUES(?,?,?)
		 ON CONFLICT(username) DO UPDATE SET pass_hash=excluded.pass_hash`,
		username, string(hash), time.Now().Unix())
	return err
}

// VerifyTech reports whether username/password match a stored technician.
func (s *Store) VerifyTech(username, password string) bool {
	var hash string
	err := s.db.QueryRow(`SELECT pass_hash FROM techs WHERE username=?`, username).Scan(&hash)
	if err != nil {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// CountTechs returns how many technician logins exist (used to warn when none
// are configured yet).
func (s *Store) CountTechs() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM techs`).Scan(&n)
	return n, err
}

// --- devices ---

// UpsertDevice records a device's latest hello details and bumps last_seen.
// The nickname is preserved across upserts.
func (s *Store) UpsertDevice(d protocol.Device) error {
	_, err := s.db.Exec(
		`INSERT INTO devices(id, hostname, username, os, version, last_seen)
		 VALUES(?,?,?,?,?,?)
		 ON CONFLICT(id) DO UPDATE SET
		   hostname=excluded.hostname,
		   username=excluded.username,
		   os=excluded.os,
		   version=excluded.version,
		   last_seen=excluded.last_seen`,
		d.ID, d.Hostname, d.Username, d.OS, d.Version, time.Now().Unix())
	return err
}

// TouchDevice updates only last_seen.
func (s *Store) TouchDevice(id string) {
	_, _ = s.db.Exec(`UPDATE devices SET last_seen=? WHERE id=?`, time.Now().Unix(), id)
}

// SetNickname sets a friendly label for a device.
func (s *Store) SetNickname(id, nickname string) error {
	_, err := s.db.Exec(`UPDATE devices SET nickname=? WHERE id=?`, nickname, id)
	return err
}

// ListDevices returns every known device (online flag is layered on by the hub
// from live socket state).
func (s *Store) ListDevices() ([]protocol.Device, error) {
	rows, err := s.db.Query(
		`SELECT id, hostname, username, os, version, nickname, last_seen
		 FROM devices ORDER BY last_seen DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []protocol.Device
	for rows.Next() {
		var d protocol.Device
		if err := rows.Scan(&d.ID, &d.Hostname, &d.Username, &d.OS, &d.Version, &d.Nickname, &d.LastSeen); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetDevice fetches one device by id.
func (s *Store) GetDevice(id string) (protocol.Device, bool) {
	var d protocol.Device
	err := s.db.QueryRow(
		`SELECT id, hostname, username, os, version, nickname, last_seen
		 FROM devices WHERE id=?`, id).
		Scan(&d.ID, &d.Hostname, &d.Username, &d.OS, &d.Version, &d.Nickname, &d.LastSeen)
	if err != nil {
		return protocol.Device{}, false
	}
	return d, true
}

// --- audit ---

// Audit appends one event. Failures are swallowed (logging must never break a
// session), but surfaced to the server log by the caller if desired.
func (s *Store) Audit(event, tech, device, sessionID, detail string) {
	_, _ = s.db.Exec(
		`INSERT INTO audit_log(ts, event, tech, device, session_id, detail)
		 VALUES(?,?,?,?,?,?)`,
		time.Now().Unix(), event, tech, device, sessionID, detail)
}

// AuditEntry is one row of the audit log, newest first via RecentAudit.
type AuditEntry struct {
	TS        int64  `json:"ts"`
	Event     string `json:"event"`
	Tech      string `json:"tech"`
	Device    string `json:"device"`
	SessionID string `json:"sessionId"`
	Detail    string `json:"detail"`
}

// RecentAudit returns up to limit newest audit entries.
func (s *Store) RecentAudit(limit int) ([]AuditEntry, error) {
	rows, err := s.db.Query(
		`SELECT ts, event, tech, device, session_id, detail
		 FROM audit_log ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.TS, &e.Event, &e.Tech, &e.Device, &e.SessionID, &e.Detail); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
