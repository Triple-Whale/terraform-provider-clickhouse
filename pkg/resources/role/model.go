package resourcerole

import (
	"sort"
)

type Grant struct {
	Database   string
	Table      string
	Privileges []string
}

type Permission struct {
	Database  string
	Table     string
	Privilege string
}

type CHRole struct {
	Name   string
	Grants []Grant
}

type RoleResource struct {
	Name    string
	Cluster string
	Grants  []Grant
}

func permissions(grants []Grant) []Permission {
	unique := map[Permission]bool{}
	for _, grant := range grants {
		for _, privilege := range grant.Privileges {
			unique[Permission{Database: grant.Database, Table: grant.Table, Privilege: privilege}] = true
		}
	}
	for candidate := range unique {
		for broader := range unique {
			if broader != candidate && broader.Privilege == candidate.Privilege && covers(broader, candidate) {
				delete(unique, candidate)
				break
			}
		}
	}
	result := make([]Permission, 0, len(unique))
	for permission := range unique {
		result = append(result, permission)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Database != result[j].Database {
			return result[i].Database < result[j].Database
		}
		if result[i].Table != result[j].Table {
			return result[i].Table < result[j].Table
		}
		return result[i].Privilege < result[j].Privilege
	})
	return result
}

func covers(broader, narrower Permission) bool {
	return (broader.Database == "*" || broader.Database == narrower.Database) &&
		(broader.Table == "*" || broader.Table == narrower.Table)
}

func sameGrants(left, right []Grant) bool {
	a, b := permissions(left), permissions(right)
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

func grantsFromPermissions(values []Permission) []Grant {
	result := []Grant{}
	for _, value := range values {
		last := len(result) - 1
		if last >= 0 && result[last].Database == value.Database && result[last].Table == value.Table {
			result[last].Privileges = append(result[last].Privileges, value.Privilege)
			continue
		}
		result = append(result, Grant{Database: value.Database, Table: value.Table, Privileges: []string{value.Privilege}})
	}
	return result
}
