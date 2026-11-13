package resourcerole

import "github.com/Triple-Whale/terraform-provider-clickhouse/pkg/grants"

type Grant = grants.Grant

type CHRole struct {
	Name   string
	Grants []Grant
}

type RoleResource struct {
	Name    string
	Cluster string
	Grants  []Grant
}
