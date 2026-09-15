package mysql

import "testing"

func TestIssue391LeadingIdentifierTokenRejectsEmptyValue(t *testing.T) {
	token, remainder, ok := leadingIdentifierToken("")
	if token != "" || remainder != "" || ok {
		t.Fatalf(`leadingIdentifierToken("") = %q %q %v, want "" "" false`, token, remainder, ok)
	}
}

func TestIssue391SplitDDLTargetAndRestRejectsTruncatedTargets(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "empty rest", value: ""},
		{name: "blank rest", value: "   "},
		{name: "quoted identifier then separate dot", value: "`t` ."},
		{name: "quoted identifier with trailing dot", value: "`t`."},
		{name: "bare identifier then separate dot", value: "t ."},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target, rest, ok := splitDDLTargetAndRest(test.value)
			if target != "" || rest != "" || ok {
				t.Fatalf("splitDDLTargetAndRest(%q) = %q %q %v, want \"\" \"\" false", test.value, target, rest, ok)
			}
		})
	}
}

func TestIssue391ExtractStatementTargetSurvivesTruncatedStatements(t *testing.T) {
	statements := []string{
		"select from",
		"select * from",
		"select 1 from",
		"select 1 union select from",
		"select 1 from `t` .",
		"select 1 from `t`.",
		"delete from",
		"insert into",
		"update",
		"update `t` .",
		"create table",
		"create table `t` .",
		"drop table",
		"truncate table",
		"alter table",
		"alter table `t` .",
		"rename table",
		"replace into `t` .",
	}
	for _, statement := range statements {
		if target := extractStatementTarget(statement); target != "" {
			t.Errorf("extractStatementTarget(%q) = %q, want empty", statement, target)
		}
	}
}
