package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/spf13/cobra"

	"github.com/pravo-systems/pravo/internal/config"
	"github.com/pravo-systems/pravo/internal/schema"
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
// message says how to write it. A command that only groups subcommands says that
// the word is not one of them.
func noArgs(cmd *cobra.Command, args []string) error {
	return rejectArgs(cmd, args, cmd.HasSubCommands())
}

// noArgsBesidesSubcommands is noArgs for a command that does its own work and has
// subcommands too, like "plan": a word that is not a subcommand is a stray argument.
func noArgsBesidesSubcommands(cmd *cobra.Command, args []string) error {
	return rejectArgs(cmd, args, false)
}

func rejectArgs(cmd *cobra.Command, args []string, groups bool) error {
	if len(args) == 0 {
		return nil
	}
	path := cmd.CommandPath()
	if groups {
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

// hclHint is the hint for a specification that declares no resource and no
// provider and has fields named like blocks of HCL; "" for any other.
func hclHint(root *config.Root) string {
	names := root.HCLFieldNames()
	if len(names) == 0 {
		return ""
	}
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = "\"" + n + "\""
	}
	subject := "The field " + quoted[0] + " is named like a block"
	if len(names) > 1 {
		subject = "The fields " + strings.Join(quoted, ", ") + " are named like blocks"
	}
	return "hint: this specification declares no resources. " + subject + " of HCL, but pravo has no blocks and does not read them. " +
		"A resource is built through a provider:\n" +
		"  $random: rnd.#Provider\n  pet: $random.#Resource.random_pet & {length: 2}\n" +
		"with rnd imported from \"pravo.dev/providers/hashicorp/random\". See https://pravo.systems/reference/spec-overview.html"
}
