package pravo_test

import (
	"strings"
	"testing"

	"github.com/pravo-systems/pravo/pravo"
)

func TestRolesOfAReadersFile(t *testing.T) {
	rs, err := pravo.ParseReaders(strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if pravo.RolesOn(rs) {
		t.Error("an empty readers file turns roles on")
	}
	if !pravo.RoleAdmin.Allows(pravo.RoleWriter) || pravo.RoleReader.Allows(pravo.RoleWriter) {
		t.Error("the roles are not ordered reader < writer < admin")
	}
}
