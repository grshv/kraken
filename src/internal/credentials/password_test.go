package credentials

import (
	"strings"
	"testing"
)

func TestHashesAreSaltedAndVerify(t *testing.T) {
	first, e := Hash("test-only-long-password")
	if e != nil {
		t.Fatal(e)
	}
	second, e := Hash("test-only-long-password")
	if e != nil {
		t.Fatal(e)
	}
	if first == second || !strings.HasPrefix(first, "$argon2id$") || !Matches(first, "test-only-long-password") || Matches(first, "wrong-password") {
		t.Fatal("salt/hash verification failure")
	}
	if Matches("$argon2id$v=19$m=99999999,t=2,p=1$bad$bad", "test-only-long-password") {
		t.Fatal("unbounded hash accepted")
	}
}
