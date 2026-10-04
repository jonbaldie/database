package catalog

import "testing"

func TestSameIdentifierUsesCanonicalCaselessMatching(t *testing.T) {
	cases := []struct {
		name  string
		left  string
		right string
		same  bool
	}{
		{name: "full case fold", left: "STRASSE", right: "straße", same: true},
		{name: "canonical normalization", left: "Å", right: "Å", same: true},
		{name: "compatibility forms stay distinct", left: "Ａ", right: "A", same: false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := SameIdentifier(test.left, test.right); got != test.same {
				t.Fatalf("SameIdentifier(%q, %q) = %v, want %v", test.left, test.right, got, test.same)
			}
		})
	}
}

func TestIsInformationSchemaUsesIdentifierMatching(t *testing.T) {
	for _, name := range []string{"information_schema", "INFORMATION_SCHEMA", "information_ſchema"} {
		if !IsInformationSchema(name) {
			t.Errorf("IsInformationSchema(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"information_schemax", "mysql"} {
		if IsInformationSchema(name) {
			t.Errorf("IsInformationSchema(%q) = true, want false", name)
		}
	}
}

func TestAccountNamesRemainCaseSensitive(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Rows().Close()

	for _, name := range []string{"Alice", "alice"} {
		if err := store.CreateAccount(Account{Name: name, PasswordHash: "hash"}); err != nil {
			t.Fatalf("create account %q: %v", name, err)
		}
		if account, found := store.Account(name); !found || account.Name != name {
			t.Fatalf("Account(%q) = %#v, %v, want exact account", name, account, found)
		}
	}
}
