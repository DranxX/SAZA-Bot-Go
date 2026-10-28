package store

import "sync"

var (
	ownerMu     sync.RWMutex
	ownerLid    string
	phoneLidMap = make(map[string]string) // phone → lid
	lidPhoneMap = make(map[string]string) // lid   → phone
)

// SetOwnerLid stores the owner's resolved LID.
func SetOwnerLid(lid string) {
	ownerMu.Lock()
	ownerLid = lid
	ownerMu.Unlock()
}

// GetOwnerLid returns the owner's cached LID.
func GetOwnerLid() string {
	ownerMu.RLock()
	defer ownerMu.RUnlock()
	return ownerLid
}

// MapLidToPhone registers a bidirectional LID ↔ phone mapping.
func MapLidToPhone(cleanLid, cleanPhone string) {
	if cleanLid == "" || cleanPhone == "" {
		return
	}
	ownerMu.Lock()
	lidPhoneMap[cleanLid] = cleanPhone
	phoneLidMap[cleanPhone] = cleanLid
	ownerMu.Unlock()
}

// IsOwnerPhone checks if a cleaned phone number (or LID number) belongs to the owner.
func IsOwnerPhone(cleanJid, cleanOwner string) bool {
	if cleanJid == cleanOwner {
		return true
	}
	ownerMu.RLock()
	defer ownerMu.RUnlock()
	if phone, ok := lidPhoneMap[cleanJid]; ok {
		return phone == cleanOwner
	}
	return false
}

// GetPhoneForLid returns the phone number mapped to a LID.
func GetPhoneForLid(lid string) string {
	ownerMu.RLock()
	defer ownerMu.RUnlock()
	return lidPhoneMap[lid]
}
