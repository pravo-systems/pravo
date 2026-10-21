package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/spf13/cobra"

	"pravo.dev/pravo/internal/schema"
)

func typesOf(ps *schema.ProviderSchema) map[string]tftypes.Type {
	out := map[string]tftypes.Type{}
	for name, s := range ps.Resources {
		out[name] = s.Block.Type()
	}
	return out
}

func tftypesFromJSON(attrs json.RawMessage, s *schema.Schema) (tftypes.Value, error) {
	return tftypes.ValueFromJSON(attrs, s.Block.Type())
}

// noArgs is the argument rule of a command that takes none: a stray word is an
// error and not something to ignore. A flag whose value is optional takes it
// only in the form -flag=value, so a value after a space ends up here; the
// message says how to write it.
func noArgs(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	path := cmd.CommandPath()
	if cmd.HasSubCommands() {
		return fmt.Errorf("unknown command %q for %q; run \"%s -h\" for the commands", args[0], path, path)
	}
	err := fmt.Errorf("%s takes no arguments, but got %q", path, strings.Join(args, " "))
	if hint := refreshValueHint(cmd, args[0]); hint != "" {
		err = fmt.Errorf("%w; %s", err, hint)
	}
	return err
}

// refreshValueHint explains a word that looks like the value of -refresh, which
// is written -refresh=value. It returns "" for any other word.
func refreshValueHint(cmd *cobra.Command, word string) string {
	f := cmd.Flags().Lookup("refresh")
	if f == nil || f.NoOptDefVal == "" || word == "" {
		return ""
	}
	if _, err := parseRefresh(word); err != nil {
		return ""
	}
	return fmt.Sprintf("write the value of -refresh with =, as in -refresh=%s", word)
}
