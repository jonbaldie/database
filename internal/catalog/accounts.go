package catalog

import "errors"

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
	return s.mutate(func(definition *Definition) error {
		account, found := definition.Accounts[name]
		if !found {
			return errors.New("account does not exist")
		}
		if err := change(&account); err != nil {
			return err
		}
		definition.Accounts[name] = account
		return nil
	})
}

func (s *Store) DeleteAccount(name string) error {
	return s.mutate(func(definition *Definition) error {
		if _, found := definition.Accounts[name]; !found {
			return errors.New("account does not exist")
		}
		delete(definition.Accounts, name)
		return nil
	})
}
