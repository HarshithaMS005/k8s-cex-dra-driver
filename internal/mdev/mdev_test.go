package mdev

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newMatrixFixture creates an mdev directory with the write-only assignment
// attributes present and points the package at it for the test's duration.
func newMatrixFixture(t *testing.T, uuid string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "matrix")
	dir := filepath.Join(root, uuid)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	for _, attr := range []string{"assign_adapter", "assign_domain", "assign_control_domain"} {
		if err := os.WriteFile(filepath.Join(dir, attr), nil, 0o600); err != nil {
			t.Fatalf("create %s: %v", attr, err)
		}
	}

	saved := VFIOAPMatrixPath
	VFIOAPMatrixPath = root
	t.Cleanup(func() { VFIOAPMatrixPath = saved })

	return dir
}

func TestAssignControlDomain(t *testing.T) {
	const uuid = "11111111-2222-3333-4444-555555555555"
	dir := newMatrixFixture(t, uuid)

	if err := AssignControlDomain(uuid, "0002"); err != nil {
		t.Fatalf("AssignControlDomain: %v", err)
	}

	// The kernel parses the value with kstrtoul(buf, 0, ...), so the 0x prefix
	// is what makes "0002" read as hex rather than as decimal 2.
	got, err := os.ReadFile(filepath.Join(dir, "assign_control_domain"))
	if err != nil {
		t.Fatalf("read assign_control_domain: %v", err)
	}
	if string(got) != "0x0002" {
		t.Errorf("assign_control_domain = %q, want %q", got, "0x0002")
	}
}

func TestAssignControlDomainNoSuchMdev(t *testing.T) {
	newMatrixFixture(t, "11111111-2222-3333-4444-555555555555")

	err := AssignControlDomain("00000000-0000-0000-0000-000000000000", "0002")
	if err == nil {
		t.Fatal("AssignControlDomain succeeded for an mdev that does not exist")
	}
	// The caller sees which domain and which mdev failed, not just ENOENT.
	if !strings.Contains(err.Error(), "0x0002") {
		t.Errorf("err = %q, want it to name the domain", err)
	}
}
