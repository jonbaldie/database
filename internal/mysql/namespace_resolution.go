package mysql

import (
	"strings"

	"github.com/jonbaldie/database/internal/catalog"
)

// namespaceResolution is the result shared by namespace existence, metadata,
// and access checks. Name visibility is enough for namespace administration;
// definition visibility is required before a caller can inspect or use data.
type namespaceResolution struct {
	namespace         catalog.Namespace
	name              string
	exists            bool
	nameVisible       bool
	definitionVisible bool
}

func resolveNamespace(definition catalog.Definition, username, name string) namespaceResolution {
	resolution := namespaceResolution{name: name}
	if strings.EqualFold(name, informationSchemaName) {
		resolution.namespace = catalog.Namespace{Name: informationSchemaName}
		resolution.exists = true
		resolution.nameVisible = true
		resolution.definitionVisible = true
		return resolution
	}

	key := catalog.Key(name)
	namespace, found := definition.Namespaces[key]
	if !found {
		return resolution
	}
	if namespace.Name == "" {
		namespace.Name = name
	}
	resolution.namespace = namespace
	resolution.exists = true
	if username == "" {
		resolution.nameVisible = true
		resolution.definitionVisible = true
		return resolution
	}
	account := definition.Accounts[username]
	resolution.definitionVisible = accountSeesNamespace(account, namespace.Name)
	resolution.nameVisible = resolution.definitionVisible || accountSeesAllNamespaces(account)
	return resolution
}

func (resolution namespaceResolution) requireName() error {
	return resolution.require(false)
}

func (resolution namespaceResolution) requireDefinition() error {
	return resolution.require(true)
}

func (resolution namespaceResolution) require(definition bool) error {
	if !resolution.exists {
		return sqlFailure{1049, "42000", "unknown database '" + resolution.name + "'"}
	}
	if !resolution.nameVisible || definition && !resolution.definitionVisible {
		return sqlFailure{1044, "42000", "access denied"}
	}
	return nil
}
