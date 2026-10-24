package catalog

import "errors"

// InitialAdministrator returns the account and grants created by database init.
func InitialAdministrator(name, passwordHash string) Account {
	return Account{
		Name:         name,
		PasswordHash: passwordHash,
		Grants: []Grant{
			{Privilege: "ACCOUNT_MANAGER"},
			{Privilege: "NAMESPACE_MANAGER"},
			{Privilege: "OPERATIONAL_OBSERVATION"},
			{Privilege: "OPERATIONAL_CONTROL"},
		},
	}
}

// MigrateInitialAdministrator imports a legacy init credential only when the
// catalog has no accounts. It never restores an account beside existing ones.
func (s *Store) MigrateInitialAdministrator(name, passwordHash string) (bool, error) {
	if name == "" || passwordHash == "" {
		return false, errors.New("legacy initial administrator is incomplete")
	}
	migrated := false
	err := s.mutate(func(definition *Definition) error {
		if len(definition.Accounts) != 0 {
			return nil
		}
		definition.Accounts[name] = InitialAdministrator(name, passwordHash)
		migrated = true
		return nil
	})
	return migrated, err
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
