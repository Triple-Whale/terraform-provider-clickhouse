package resourceuser

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"

	"github.com/Triple-Whale/terraform-provider-clickhouse/pkg/grants"
)

type CHUser struct {
	Name               string
	Storage            string
	AuthTypes          []string
	Roles              []string
	DefaultRoles       []string
	DefaultRolesAll    uint8
	DefaultRolesExcept []string
	AdminRoles         []string
	Grants             []grants.Grant
}

type UserResource struct {
	Name         string
	Cluster      string
	Password     string
	Roles        []string
	DefaultRoles []string
	Grants       []grants.Grant
}

func passwordHash(password string) string {
	hash := sha256.Sum256([]byte(password))
	return hex.EncodeToString(hash[:])
}

func sorted(values []string) []string {
	result := append([]string{}, values...)
	sort.Strings(result)
	return result
}

func equalStrings(a, b []string) bool {
	a, b = sorted(a), sorted(b)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func includes(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func (u *CHUser) effectiveDefaults() []string {
	if u.DefaultRolesAll == 0 {
		return sorted(u.DefaultRoles)
	}
	result := []string{}
	for _, role := range u.Roles {
		if !includes(u.DefaultRolesExcept, role) {
			result = append(result, role)
		}
	}
	return sorted(result)
}

func (u *CHUser) canonical() bool {
	return u.DefaultRolesAll == 0 && len(u.DefaultRolesExcept) == 0 && len(u.AdminRoles) == 0 && equalStrings(u.AuthTypes, []string{"sha256_password"})
}

func sameUser(a, b *CHUser) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Storage == b.Storage && equalStrings(a.Roles, b.Roles) && equalStrings(a.effectiveDefaults(), b.effectiveDefaults()) && grants.Equal(a.Grants, b.Grants) && a.canonical() && b.canonical()
}

func matchesPlan(user *CHUser, plan UserResource) bool {
	return user != nil && user.canonical() && equalStrings(user.Roles, plan.Roles) && equalStrings(user.DefaultRoles, plan.DefaultRoles) && grants.Equal(user.Grants, plan.Grants)
}
