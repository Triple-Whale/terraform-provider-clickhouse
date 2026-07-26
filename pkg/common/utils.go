package common

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func toTablePhrase(toTable *string) string {
	if toTable != nil {
		return fmt.Sprintf(`,"to_table":"%v"`, *toTable)
	}
	return ""
}

func refreshPhrase(refresh *string) string {
	if refresh != nil && *refresh != "" {
		return fmt.Sprintf(`,"refresh":"%v"`, *refresh)
	}
	return ""
}

func GetComment(comment string, cluster string, toTable *string, refresh *string) string {
	storingComment := fmt.Sprintf(`{"comment":"%v","cluster":"%v"%s%s}`, comment, cluster, toTablePhrase(toTable), refreshPhrase(refresh))
	storingComment = strings.Replace(storingComment, "'", "\\'", -1)
	return storingComment
}

func UnmarshalComment(storedComment string) (comment string, cluster string, toTable string, refresh string, err error) {
	if storedComment == "" {
		return "", "", "", "", nil
	}
	storedComment = strings.Replace(storedComment, "\\'", "'", -1)

	byteStreamComment := []byte(storedComment)

	var dat map[string]interface{}

	if err := json.Unmarshal(byteStreamComment, &dat); err != nil {
		return "", "", "", "", err
	}
	comment = dat["comment"].(string)
	cluster = dat["cluster"].(string)
	if dat["to_table"] != nil {
		toTable = dat["to_table"].(string)
	}
	if dat["refresh"] != nil {
		refresh = dat["refresh"].(string)
	}

	return comment, cluster, toTable, refresh, err
}

func GetClusterStatement(cluster string) (clusterStatement string) {
	if cluster != "" {
		return fmt.Sprintf("ON CLUSTER %s", cluster)
	}
	return ""
}

// Quote all strings on a string slice
func Quote(elems []string) []string {
	var quotedElems []string
	for _, elem := range elems {
		quotedElems = append(quotedElems, fmt.Sprintf("%q", elem))
	}
	return quotedElems
}

func StringSetToList(set *schema.Set) []string {
	var list []string
	for _, item := range set.List() {
		list = append(list, item.(string))
	}
	return list
}

func StringListToSet(list []string) *schema.Set {
	var set []interface{}
	for _, item := range list {
		set = append(set, item)
	}
	return schema.NewSet(schema.HashString, set)
}

func FormatSQL(sql string) string {
	cmd := exec.Command("clickhouse", "format", "--query", sql)

	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	err := cmd.Run()

	if err != nil {
		return sql
	}

	ret := out.String()
	ret = strings.Trim(ret, "\n")

	return ret
}
