package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// BanEntry represents a single banned user.
type BanEntry struct {
	JID    string `json:"jid"`
	Number string `json:"number"`
	Lid    string `json:"lid,omitempty"`
	Expiry *int64 `json:"expiry,omitempty"` // UnixMilli, nil = permanent
	Reason string `json:"reason,omitempty"`
}

var (
	banMu   sync.Mutex
	banDB   *sql.DB
	banPath string
)

// InitBanned opens or creates the SQLite ban database at db/banned/banned.db.
func InitBanned(path string) {
	banPath = path
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		fmt.Println("[BANNED] Failed to create directory:", err)
		return
	}
	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=5000")
	if err != nil {
		fmt.Println("[BANNED] Failed to open DB:", err)
		return
	}
	banDB = db
	_, _ = banDB.Exec(`
		CREATE TABLE IF NOT EXISTS banned (
			jid    TEXT PRIMARY KEY,
			number TEXT NOT NULL,
			expiry INTEGER,
			lid    TEXT,
			reason TEXT
		);
		CREATE INDEX IF NOT EXISTS idx_banned_number ON banned(number);
		CREATE INDEX IF NOT EXISTS idx_banned_lid ON banned(lid);
	`)
	cleanExpiredBans()
}

func cleanExpiredBans() {
	if banDB == nil {
		return
	}
	now := time.Now().UnixMilli()
	_, _ = banDB.Exec(`DELETE FROM banned WHERE expiry IS NOT NULL AND expiry <= ?`, now)
}

// IsBanned returns true if the given phone/lid is on the ban list.
func IsBanned(phone, lid string) bool {
	if banDB == nil {
		return false
	}
	banMu.Lock()
	defer banMu.Unlock()

	cleanExpiredBans()
	jid := phone + "@s.whatsapp.net"
	now := time.Now().UnixMilli()

	rows, err := banDB.Query(`SELECT jid, number, lid, expiry FROM banned`)
	if err != nil {
		return false
	}
	defer rows.Close()

	for rows.Next() {
		var eJid, eNumber, eLid string
		var eExpiry *int64
		_ = rows.Scan(&eJid, &eNumber, &eLid, &eExpiry)
		if eExpiry != nil && *eExpiry <= now {
			continue
		}
		if eJid == jid || (eNumber != "" && eNumber == phone) || (eLid != "" && eLid == lid) {
			return true
		}
	}
	return false
}

// AddBan adds or updates a ban. durationMs=0 means permanent.
func AddBan(phone, lid, reason string, durationMs int64) {
	if banDB == nil {
		return
	}
	banMu.Lock()
	defer banMu.Unlock()

	cleanExpiredBans()
	jid := phone + "@s.whatsapp.net"
	var expiry *int64
	if durationMs > 0 {
		t := time.Now().UnixMilli() + durationMs
		expiry = &t
	}

	var existingJid string
	_ = banDB.QueryRow(`SELECT jid FROM banned WHERE jid=? OR (number=? AND number!='') OR (lid=? AND lid!='')`,
		jid, phone, lid).Scan(&existingJid)

	if existingJid != "" {
		_, _ = banDB.Exec(`UPDATE banned SET jid=?, number=?, expiry=?, lid=?, reason=? WHERE jid=?`,
			jid, phone, expiry, lid, reason, existingJid)
	} else {
		_, _ = banDB.Exec(`INSERT INTO banned (jid, number, expiry, lid, reason) VALUES (?, ?, ?, ?, ?)`,
			jid, phone, expiry, lid, reason)
	}
}

// RemoveBan removes a ban entry. Returns false if not found.
func RemoveBan(phone string) bool {
	if banDB == nil {
		return false
	}
	banMu.Lock()
	defer banMu.Unlock()

	cleanExpiredBans()
	jid := phone
	if !strings.Contains(phone, "@") {
		jid = phone + "@s.whatsapp.net"
	}
	res, _ := banDB.Exec(`DELETE FROM banned WHERE jid=? OR number=? OR lid=?`, jid, phone, phone)
	n, _ := res.RowsAffected()
	return n > 0
}

// ListBanned returns the current non-expired ban list.
func ListBanned() []BanEntry {
	if banDB == nil {
		return nil
	}
	banMu.Lock()
	defer banMu.Unlock()

	cleanExpiredBans()
	rows, err := banDB.Query(`SELECT jid, number, lid, expiry, reason FROM banned ORDER BY jid`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var list []BanEntry
	for rows.Next() {
		var e BanEntry
		_ = rows.Scan(&e.JID, &e.Number, &e.Lid, &e.Expiry, &e.Reason)
		list = append(list, e)
	}
	return list
}
