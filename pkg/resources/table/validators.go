package resourcetable

import (
	"fmt"

	v "github.com/go-playground/validator/v10"
	hashicorpcty "github.com/hashicorp/go-cty/cty"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
)

func ValidatePartitionBy(inValue any, p hashicorpcty.Path) diag.Diagnostics {
	validate := v.New()
	value := inValue.(string)
	toAllowedPartitioningFunctions := "toYYYYMM toYYYYMMDD toYYYYMMDDhhmmss"
	validation := fmt.Sprintf("oneof=%v", toAllowedPartitioningFunctions)
	var diags diag.Diagnostics
	if validate.Var(value, validation) != nil {
		diag := diag.Diagnostic{
			Severity: diag.Error,
			Summary:  "wrong value",
			Detail:   fmt.Sprintf("%q is not %q", value, toAllowedPartitioningFunctions),
		}
		diags = append(diags, diag)
	}
	return diags
}

func ValidateOnClusterEngine(inValue any, p hashicorpcty.Path) diag.Diagnostics {
	validate := v.New()
	value := inValue.(string)
	mergeTreeTypes := "ReplacingMergeTree"
	replicatedTypes := "ReplicatedMergeTree"
	replicatedReplacingTypes := "ReplicatedReplacingMergeTree"
	distributedTypes := "Distributed"
	kafkaTypes := "Kafka"
	validation := fmt.Sprintf("oneof=%v %v %v %v %v", replicatedTypes, distributedTypes, mergeTreeTypes, replicatedReplacingTypes, kafkaTypes)
	var diags diag.Diagnostics
	if validate.Var(value, validation) != nil {
		diag := diag.Diagnostic{
			Severity: diag.Error,
			Summary:  "wrong value",
			Detail:   fmt.Sprintf("%q is not %q %q %q %q %q", value, replicatedTypes, distributedTypes, mergeTreeTypes, replicatedReplacingTypes, kafkaTypes),
		}
		diags = append(diags, diag)
	}
	return diags
}
