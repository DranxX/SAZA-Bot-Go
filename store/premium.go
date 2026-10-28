package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// PremiumEntry represents a premium user.
type PremiumEntry struct {
	JID          string `json:"jid"`
	Number       string `json:"number"`
	Lid          string `json:"lid,omitempty"`
	Expiry       *int64 `json:"expiry,omitempty"`
	Credits      int    `json:"credits"`
	CreditPeriod string `json:"creditPeriod"`
	AddedAt      int64  `json:"addedAt"`
	UpdatedAt    int64  `json:"updatedAt"`
}

// PremiumProfile is a premium entry with an explicit IsPremium flag.
type PremiumProfile struct {
	PremiumEntry
	IsPremium bool
}

// PremiumResult is returned by premium store operations.
type PremiumResult struct {
	OK      bool
	Created bool
	Error   string
	Entry   PremiumProfile
}

const MonthlyPremiumCredits = 10

var (
	premMu   sync.Mutex
	premDB   *sql.DB
	premPath string
)

// InitPremium opens or creates the SQLite premium database at db/premium/premium.db.
func InitPremium(path string) {
	premPath = path
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		fmt.Println("[PREMIUM] Failed to create directory:", err)
		return
	}
	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=5000")
	if err != nil {
		fmt.Println("[PREMIUM] Failed to open DB:", err)
		return
	}
	premDB = db
	_, _ = premDB.Exec(`
		CREATE TABLE IF NOT EXISTS premium (
			jid           TEXT PRIMARY KEY,
			number        TEXT NOT NULL,
			expiry        INTEGER,
			lid           TEXT,
			credits       INTEGER NOT NULL DEFAULT 10,
			credit_period TEXT NOT NULL,
			added_at      INTEGER NOT NULL,
			updated_at    INTEGER NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_premium_number ON premium(number);
		CREATE INDEX IF NOT EXISTS idx_premium_lid ON premium(lid);
	`)
}

func currentPeriod() string {
	t := time.Now()
	return fmt.Sprintf("%04d-%02d", t.Year(), t.Month())
}

func queryPremiumEntry(query, arg1, arg2 string) (*PremiumEntry, error) {
	if premDB == nil {
		return nil, fmt.Errorf("DB not initialized")
	}
	var e PremiumEntry
	err := premDB.QueryRow(query, arg1, arg2).Scan(
		&e.JID, &e.Number, &e.Expiry, &e.Lid, &e.Credits, &e.CreditPeriod, &e.AddedAt, &e.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func cleanExpiredPremium() {
	if premDB == nil {
		return
	}
	now := time.Now().UnixMilli()
	_, _ = premDB.Exec(`DELETE FROM premium WHERE expiry IS NOT NULL AND expiry <= ?`, now)
}

// GetPremiumProfile returns a user's premium profile.
func GetPremiumProfile(phone, lid string) PremiumProfile {
	if premDB == nil {
		return PremiumProfile{}
	}
	premMu.Lock()
	defer premMu.Unlock()

	cleanExpiredPremium()
	jid := phone + "@s.whatsapp.net"

	e, err := queryPremiumEntry(`SELECT jid, number, expiry, lid, credits, credit_period, added_at, updated_at FROM premium WHERE jid=?`, jid, "")
	if err != nil && lid != "" {
		e, err = queryPremiumEntry(`SELECT jid, number, expiry, lid, credits, credit_period, added_at, updated_at FROM premium WHERE lid=?`, lid, "")
	}
	if err != nil && phone != "" {
		e, err = queryPremiumEntry(`SELECT jid, number, expiry, lid, credits, credit_period, added_at, updated_at FROM premium WHERE number=?`, phone, "")
	}
	if err != nil {
		return PremiumProfile{PremiumEntry: PremiumEntry{Number: phone, JID: jid, Lid: lid}}
	}

	p := currentPeriod()
	if e.CreditPeriod != p {
		e.CreditPeriod = p
		e.Credits = MonthlyPremiumCredits
		e.UpdatedAt = time.Now().UnixMilli()
		_, _ = premDB.Exec(`UPDATE premium SET credit_period=?, credits=?, updated_at=? WHERE jid=?`, p, e.Credits, e.UpdatedAt, e.JID)
	}

	return PremiumProfile{PremiumEntry: *e, IsPremium: true}
}

// ListPremiumUsers returns all premium users.
func ListPremiumUsers() []PremiumProfile {
	if premDB == nil {
		return nil
	}
	premMu.Lock()
	defer premMu.Unlock()

	cleanExpiredPremium()
	rows, err := premDB.Query(`SELECT jid, number, expiry, lid, credits, credit_period, added_at, updated_at FROM premium ORDER BY jid`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var list []PremiumProfile
	now := time.Now().UnixMilli()
	for rows.Next() {
		var e PremiumEntry
		_ = rows.Scan(&e.JID, &e.Number, &e.Expiry, &e.Lid, &e.Credits, &e.CreditPeriod, &e.AddedAt, &e.UpdatedAt)
		if e.Expiry != nil && *e.Expiry <= now {
			continue
		}
		if e.CreditPeriod != currentPeriod() {
			e.CreditPeriod = currentPeriod()
			e.Credits = MonthlyPremiumCredits
			e.UpdatedAt = time.Now().UnixMilli()
			_, _ = premDB.Exec(`UPDATE premium SET credit_period=?, credits=?, updated_at=? WHERE jid=?`,
				e.CreditPeriod, e.Credits, e.UpdatedAt, e.JID)
		}
		list = append(list, PremiumProfile{PremiumEntry: e, IsPremium: true})
	}
	return list
}

// AddPremiumUser adds or updates a premium user. durationSec=0 → permanent.
func AddPremiumUser(phone, lid string, durationSec int64) PremiumResult {
	if premDB == nil {
		return PremiumResult{Error: "DB not initialized"}
	}
	premMu.Lock()
	defer premMu.Unlock()

	cleanExpiredPremium()
	jid := phone + "@s.whatsapp.net"
	period := currentPeriod()
	var expiry *int64
	if durationSec > 0 {
		t := time.Now().UnixMilli() + durationSec*1000
		expiry = &t
	}
	now := time.Now().UnixMilli()

	var existingJid string
	_ = premDB.QueryRow(`SELECT jid FROM premium WHERE jid=? OR number=? OR (lid=? AND lid!='')`,
		jid, phone, lid).Scan(&existingJid)

	if existingJid != "" {
		_, _ = premDB.Exec(`UPDATE premium SET jid=?, number=?, expiry=?, lid=?, updated_at=? WHERE jid=?`,
			jid, phone, expiry, lid, now, existingJid)
		e, _ := queryPremiumEntry(`SELECT jid, number, expiry, lid, credits, credit_period, added_at, updated_at FROM premium WHERE jid=?`, jid, "")
		if e != nil {
			return PremiumResult{OK: true, Entry: PremiumProfile{PremiumEntry: *e, IsPremium: true}}
		}
	} else {
		_, _ = premDB.Exec(`INSERT INTO premium (jid, number, expiry, lid, credits, credit_period, added_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			jid, phone, expiry, lid, MonthlyPremiumCredits, period, now, now)
		e, _ := queryPremiumEntry(`SELECT jid, number, expiry, lid, credits, credit_period, added_at, updated_at FROM premium WHERE jid=?`, jid, "")
		if e != nil {
			return PremiumResult{OK: true, Created: true, Entry: PremiumProfile{PremiumEntry: *e, IsPremium: true}}
		}
	}
	return PremiumResult{Error: "Failed to add premium user"}
}

// DeletePremiumUser removes a user from premium.
func DeletePremiumUser(phone string) PremiumResult {
	if premDB == nil {
		return PremiumResult{Error: "DB not initialized"}
	}
	premMu.Lock()
	defer premMu.Unlock()

	cleanExpiredPremium()
	jid := phone + "@s.whatsapp.net"

	e, _ := queryPremiumEntry(`SELECT jid, number, expiry, lid, credits, credit_period, added_at, updated_at FROM premium WHERE jid=? OR number=?`,
		jid, phone)
	if e == nil {
		return PremiumResult{Error: "User not in premium list."}
	}

	_, _ = premDB.Exec(`DELETE FROM premium WHERE jid=?`, e.JID)
	return PremiumResult{OK: true, Entry: PremiumProfile{PremiumEntry: *e, IsPremium: true}}
}

// SetPremiumCredits sets a user's credits to a specific amount.
func SetPremiumCredits(phone string, amount int) PremiumResult {
	return modifyPremCredits(phone, func(credits int) int { return max(0, amount) })
}

// AddPremiumCredits adds credits to a user.
func AddPremiumCredits(phone string, amount int) PremiumResult {
	return modifyPremCredits(phone, func(credits int) int { return credits + max(0, amount) })
}

// RemovePremiumCredits subtracts credits from a user.
func RemovePremiumCredits(phone string, amount int) PremiumResult {
	return modifyPremCredits(phone, func(credits int) int { return max(0, credits-max(0, amount)) })
}

// ConsumePremiumCredit deducts one credit; returns error if insufficient.
func ConsumePremiumCredit(phone string) PremiumResult {
	if premDB == nil {
		return PremiumResult{Error: "DB not initialized"}
	}
	premMu.Lock()
	defer premMu.Unlock()

	cleanExpiredPremium()
	jid := phone + "@s.whatsapp.net"

	e, err := queryPremiumEntry(`SELECT jid, number, expiry, lid, credits, credit_period, added_at, updated_at FROM premium WHERE jid=? OR number=?`,
		jid, phone)
	if err != nil {
		return PremiumResult{Error: "User is not premium."}
	}
	if e.Credits < 1 {
		return PremiumResult{Error: "Not enough credits.", Entry: PremiumProfile{PremiumEntry: *e, IsPremium: true}}
	}
	e.Credits--
	e.UpdatedAt = time.Now().UnixMilli()
	_, _ = premDB.Exec(`UPDATE premium SET credits=?, updated_at=? WHERE jid=?`, e.Credits, e.UpdatedAt, e.JID)
	return PremiumResult{OK: true, Entry: PremiumProfile{PremiumEntry: *e, IsPremium: true}}
}

func modifyPremCredits(phone string, fn func(credits int) int) PremiumResult {
	if premDB == nil {
		return PremiumResult{Error: "DB not initialized"}
	}
	premMu.Lock()
	defer premMu.Unlock()

	cleanExpiredPremium()
	jid := phone + "@s.whatsapp.net"

	e, err := queryPremiumEntry(`SELECT jid, number, expiry, lid, credits, credit_period, added_at, updated_at FROM premium WHERE jid=? OR number=?`,
		jid, phone)
	if err != nil {
		return PremiumResult{Error: "User is not premium."}
	}

	newCredits := fn(e.Credits)
	now := time.Now().UnixMilli()
	_, _ = premDB.Exec(`UPDATE premium SET credits=?, updated_at=? WHERE jid=?`, newCredits, now, e.JID)
	e.Credits = newCredits
	e.UpdatedAt = now
	return PremiumResult{OK: true, Entry: PremiumProfile{PremiumEntry: *e, IsPremium: true}}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
