package persistence

import (
	"strings"
	"testing"
)

func TestSplitSQLStatements_DollarQuotedBodies(t *testing.T) {
	src := strings.Join([]string{
		"BEGIN;",
		"CREATE OR REPLACE FUNCTION f() RETURNS void AS $$",
		"BEGIN;  -- 函数体内的 BEGIN 与分号",
		"  PERFORM 1;",
		"  PERFORM 2;",
		"END;",
		"$$ LANGUAGE plpgsql;",
		"DO $$ BEGIN PERFORM 3; END $$;",
		"CREATE TABLE t1 (a int);",
		"INSERT INTO t1 VALUES (1); -- 行注释里的; 不切分",
		"/* 块注释里的; 不切分 */",
		"INSERT INTO t1 VALUES (2);",
		"COMMIT;",
	}, "\n")
	got := splitSQLStatements(src)
	// 注释行不带分号时会黏附到下一条语句（合法 SQL），故期望 7 条：
	// BEGIN / FUNCTION / DO / CREATE TABLE / INSERT(1) / [行注释+块注释]+INSERT(2) / COMMIT
	want := 7
	if len(got) != want {
		t.Fatalf("got %d statements, want %d:\n%s", len(got), want, strings.Join(got, "\n|||\n"))
	}
	if !strings.Contains(got[1], "PERFORM 2;") {
		t.Errorf("function body split wrongly:\n%s", got[1])
	}
	// 注释内分号不得切分；注释体黏附在 INSERT(2) 前
	if !strings.Contains(got[5], "INSERT INTO t1 VALUES (2)") ||
		!strings.Contains(got[5], "块注释里的") || !strings.Contains(got[5], "行注释里的") {
		t.Errorf("comment handling wrong:\n%s", got[5])
	}
	if strings.TrimSpace(got[6]) != "COMMIT" {
		t.Errorf("last stmt should be COMMIT, got:\n%s", got[6])
	}
}

func TestSplitSQLStatements_QuoteEscapes(t *testing.T) {
	src := "INSERT INTO t VALUES ('a''b; c');\nSELECT 1;"
	got := splitSQLStatements(src)
	if len(got) != 2 {
		t.Fatalf("got %d statements, want 2:\n%v", len(got), got)
	}
	if !strings.Contains(got[0], "a''b; c") {
		t.Errorf("escaped quote handling wrong:\n%s", got[0])
	}
}

func TestSplitSQLStatements_EmptyAndWhitespace(t *testing.T) {
	got := splitSQLStatements("  \n  ;  \n  ")
	if len(got) != 0 {
		t.Fatalf("expected no statements, got %v", got)
	}
}

func TestIsCommentOnly(t *testing.T) {
	cases := map[string]bool{
		"   ":                      true,
		"/* only */":               true,
		"-- line\n-- again":        true,
		"SELECT 1":                 false,
		"SELECT '/* not comment'":  false,
		"SELECT '$$x$$'":           false,
		"/* c */ SELECT 2":         false,
	}
	for in, want := range cases {
		if got := isCommentOnly(in); got != want {
			t.Errorf("isCommentOnly(%q) = %v, want %v", in, got, want)
		}
	}
}
