package security

import "testing"

func TestPassword(t *testing.T) {
	h := Password("a sufficiently long password")
	if !VerifyPassword(h, "a sufficiently long password") {
		t.Fatal("valid password rejected")
	}
	if VerifyPassword(h, "wrong password") {
		t.Fatal("wrong password accepted")
	}
}
func TestVault(t *testing.T) {
	v, e := OpenVault(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	x, e := v.Encrypt("secret")
	if e != nil {
		t.Fatal(e)
	}
	got, e := v.Decrypt(x)
	if e != nil || got != "secret" {
		t.Fatalf("round trip: %q %v", got, e)
	}
}
