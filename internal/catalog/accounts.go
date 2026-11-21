package catalog

import "errors"

// AccountManagerPrivilege permits account administration at server scope.
const AccountManagerPrivilege = "ACCOUNT_MANAGER"

// ErrLastAccountManager reports an account write that would leave no enabled manager.
var ErrLastAccountManager = errors.New("last account manager is protected")

// HasGrant reports whether the account holds the privilege at the given scope.
// Lock state is independent of the grants held by an account.
func (a Account) HasGrant(privilege, namespace string) bool {
	for _, grant := range a.Grants {
		if grant.Privilege == privilege && Key(grant.Namespace) == Key(namespace) {
			return true
		}
	}
	return false
}

// UpdateAccounts atomically changes account state and protects the last enabled
// account manager. A rejected change is neither published nor persisted.
// The callback must not call Store methods or retain the staged account map.
func (s *Store) UpdateAccounts(change func(map[string]Account) error) error {
	return s.mutate(func(definition *Definition) error {
		if err := change(definition.Accounts); err != nil {
			return err
		}
		for _, account := range definition.Accounts {
			if !account.Locked && account.HasGrant(AccountManagerPrivilege, "") {
				return nil
			}
		}
		return ErrLastAccountManager
	})
}

// EnsureAccount creates the initial account once. It never changes an account
// that already exists, so an ordinary restart preserves later administration.
func (s *Store) EnsureAccount(name, passwordHash string, grants []Grant) error {
	if name == "" || passwordHash == "" {
		return nil
	}
	return s.mutate(func(definition *Definition) error {
		if _, found := definition.Accounts[name]; found {
			return nil
		}
		definition.Accounts[name] = Account{Name: name, PasswordHash: passwordHash, Grants: append([]Grant(nil), grants...)}
		return nil
	})
}

func (s *Store) Account(name string) (Account, bool) {
	definition := s.Snapshot()
	account, found := definition.Accounts[name]
	return account, found
}

func (s *Store) CreateAccount(account Account) error {
	return s.mutate(func(definition *Definition) error {
		if _, found := definition.Accounts[account.Name]; found {
			return errors.New("account exists")
		}
		definition.Accounts[account.Name] = account
		return nil
	})
}

func (s *Store) UpdateAccount(name string, change func(*Account) error) error {
	return s.UpdateAccounts(func(accounts map[string]Account) error {
		account, found := accounts[name]
		if !found {
			return errors.New("account does not exist")
		}
		if err := change(&account); err != nil {
			return err
		}
		accounts[name] = account
		return nil
	})
}

func (s *Store) DeleteAccount(name string) error {
	return s.UpdateAccounts(func(accounts map[string]Account) error {
		if _, found := accounts[name]; !found {
			return errors.New("account does not exist")
		}
		delete(accounts, name)
		return nil
	})
}
