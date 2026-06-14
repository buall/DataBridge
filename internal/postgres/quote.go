package postgres

import "strings"

func QuoteIdent(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func SplitTable(value string) (string, string, bool) {
	index := strings.IndexByte(value, '.')
	if index <= 0 || index == len(value)-1 {
		return "", "", false
	}
	return value[:index], value[index+1:], true
}
