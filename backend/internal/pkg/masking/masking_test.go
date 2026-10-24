package masking

import (
	"strings"
	"testing"
)

func TestRedactPassword(t *testing.T) {
	if got := RedactPassword("abc"); got != "****" {
		t.Fatalf("short password: %q", got)
	}
	if got := RedactPassword("P@ssw0rdLong"); got != "******Long" {
		t.Fatalf("long password: %q", got)
	}
}

func TestSanitizeForAI(t *testing.T) {
	in := "db mysql://scott:tiger@10.1.2.3:3306/db down at 10.2.3.4:5432, password: hunter22, phone 13812345678"
	out := SanitizeForAI(in)
	if strings.Contains(out, "tiger") || strings.Contains(out, "10.1.2.3") {
		t.Fatalf("conn string not redacted: %s", out)
	}
	if strings.Contains(out, "10.2.3.4") {
		t.Fatalf("host:port not redacted: %s", out)
	}
	if strings.Contains(out, "hunter22") {
		t.Fatalf("password value not redacted: %s", out)
	}
	if !strings.Contains(out, "138****5678") {
		t.Fatalf("phone not redacted: %s", out)
	}
}

func TestSanitizeForAIIDCardAndBankCard(t *testing.T) {
	out := SanitizeForAI("id 110101199003077758 card 6222020200112233445")
	if !strings.Contains(out, "1101**********7758") {
		t.Fatalf("id card not redacted: %s", out)
	}
	if !strings.Contains(out, "6222********3445") {
		t.Fatalf("bank card not redacted: %s", out)
	}
}

func TestRedactJSON(t *testing.T) {
	in := `{"password":"abc123","password_enc":"enc.v1.xyz","host":"1.2.3.4:3306"}`
	out := RedactJSON(in)
	if strings.Contains(out, "abc123") || strings.Contains(out, "enc.v1.xyz") {
		t.Fatalf("password fields not redacted: %s", out)
	}
	if strings.Contains(out, "1.2.3.4") {
		t.Fatalf("host not redacted: %s", out)
	}
	if out == "" {
		t.Fatal("empty output")
	}
}

func TestDigest(t *testing.T) {
	if got := Digest("a\nb", 10); got != "a b" {
		t.Fatalf("newline fold: %q", got)
	}
	if got := Digest(strings.Repeat("x", 50), 10); got != strings.Repeat("x", 10)+"..." {
		t.Fatalf("truncate: %q", got)
	}
}