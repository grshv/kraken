package store

import (
	"testing"
	"time"
)

func TestAcceptanceAuditRetentionThreeYears(t *testing.T) {
	if AuditRetention < 3*365*24*time.Hour {
		t.Fatalf("AuditRetention = %v, want at least 3 years", AuditRetention)
	}
}
