package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/pravo-systems/pravo/internal/engine"
	"github.com/pravo-systems/pravo/internal/serve"
)

func templateCmd(rt *runContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "template",
		Short: "List the templates of the specification, and try one with values, as the viewer's wizard does",
		Long: `The templates of the specification, read as the viewer reads them: a definition that embeds pravo.#Template.

  pravo template list                          the templates, their inputs and their instances
  pravo template try api --name checkout ...   the CUE of a new instance, checked

Both read only. They write no file, take no lock and apply nothing; the CUE that "try" prints is for a person
to paste, and the plan that follows is made and applied as any other. They run the code of the viewer's wizard in
process, so they say what the wizard says, and a sealed state is read with your reader identity as "pravo serve"
reads it. With --json each prints one JSON document.`,
		Args: noArgs,
	}
	cmd.AddCommand(templateListCmd(rt), templateTryCmd(rt))
	return cmd
}

// templateServer builds the viewer's server in process for the commands of "pravo template" and says what a refused
// call is.
func templateServer(rt *runContext, cmd *cobra.Command) (*serve.Server, func(), error) {
	v, opts, err := viewerOptions(rt)
	if err != nil {
		return nil, nil, err
	}
	srv, err := serve.New(opts)
	if err != nil {
		v.close()
		return nil, nil, engine.WithCode(engine.ExitInvalid, err)
	}
	v.started()
	return srv, v.close, nil
}

// templateCall asks the server for one path and decodes the answer into out, or turns an error answer into an error.
func templateCall(srv *serve.Server, path string, q url.Values, out any) (json.RawMessage, error) {
	status, body := srv.Call(path, q)
	if status != 200 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &e)
		if e.Error == "" {
			e.Error = "the template could not be read"
		}
		return nil, engine.WithCode(engine.ExitInvalid, errors.New(e.Error))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return nil, err
	}
	return body, nil
}

func templateListCmd(rt *runContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the templates, with their inputs and instances",
		Long: `Lists the templates of the specification in this directory: the name, the title, the definition, how many
resources one instance holds and the instances that exist. With --json the document is the one that the viewer's
Templates page reads: each template with its inputs in order, and for each input its kind, its CUE, its label,
whether it is required, its default, its choices or the type of resource to pick one of, and whether it is a secret.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			srv, closeFn, err := templateServer(rt, cmd)
			if err != nil {
				return err
			}
			defer closeFn()
			var cat serve.Templates
			raw, err := templateCall(srv, "/api/templates", nil, &cat)
			if err != nil {
				return err
			}
			if !cat.SpecOK {
				return engine.WithCode(engine.ExitInvalid, errors.New("the specification does not load, so no template is known. Run pravo validate"))
			}
			if rt.flags.JSON {
				return emitJSON(raw)
			}
			if len(cat.Templates) == 0 {
				rt.out.Println("This specification has no template. A definition that embeds pravo.#Template is one.")
				return nil
			}
			tw := tabwriter.NewWriter(rt.out.Out, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tTITLE\tDEFINITION\tINPUTS\tRESOURCES\tINSTANCES")
			for _, t := range cat.Templates {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%d\n", t.Name, t.Title, t.Definition, len(t.Inputs), t.Resources, len(t.Instances))
			}
			return tw.Flush()
		},
	}
	addTagFlags(rt, cmd)
	addAccessDirFlag(rt, cmd)
	return cmd
}

func templateTryCmd(rt *runContext) *cobra.Command {
	var name, at string
	var sets, secrets, secretEnvs []string
	cmd := &cobra.Command{
		Use:   "try TEMPLATE --name LABEL [--set INPUT=VALUE]...",
		Short: "Make the CUE of a new instance of a template and check it, without writing anything",
		Long: `Makes the CUE of a new instance of TEMPLATE named LABEL, loads the specification with it laid over, in memory,
and says what the specification and the policies in force say of it. It writes nothing: the CUE is for a person to
paste, in the file where the other instances are (the answer names it).

  --set INPUT=VALUE        a value for an input. For an input of kind string, VALUE is the text; for any other
                           kind it is CUE, such as 8080, true or ["a", "b"]. For an enum, either of them.
  --secret INPUT=NAME      the input is a secret read from the secret NAME of the state
  --secret-env INPUT=NAME  the input is a secret read from the environment variable NAME when pravo runs
  --at PATH                the struct the instance goes into, such as services (default: the top level)

A secret input never takes a value. The CUE says where the secret is read from.

The answer has a state: valid, incomplete (an input without a default has no value), invalid (the specification
does not load with the CUE) or exists (the address is taken). Exit status 0 is a valid instance that the policies
hold or that none judges, 5 is one that a policy refuses, and 6 any other state. With --json the document is the
one that the viewer's wizard reads.`,
		Example: `  pravo template try service --name checkout --set port=8081 --set image=ghcr.io/acme/checkout:1.4
  pravo template try service --name checkout --at services --secret api_key=checkout-api-key --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			srv, closeFn, err := templateServer(rt, cmd)
			if err != nil {
				return err
			}
			defer closeFn()
			var cat serve.Templates
			if _, err := templateCall(srv, "/api/templates", nil, &cat); err != nil {
				return err
			}
			if !cat.SpecOK {
				return engine.WithCode(engine.ExitInvalid, errors.New("the specification does not load, so no template is known. Run pravo validate"))
			}
			var tpl *serve.TemplateEntry
			for i := range cat.Templates {
				if cat.Templates[i].Name == args[0] {
					tpl = &cat.Templates[i]
				}
			}
			if tpl == nil {
				names := make([]string, 0, len(cat.Templates))
				for _, t := range cat.Templates {
					names = append(names, t.Name)
				}
				sort.Strings(names)
				hint := "the specification has no template"
				if len(names) > 0 {
					hint = "the templates are " + strings.Join(names, ", ")
				}
				return engine.WithCode(engine.ExitInvalid, fmt.Errorf("no template %q: %s", args[0], hint))
			}
			q := url.Values{"template": {args[0]}, "name": {name}}
			if at != "" {
				q.Set("at", at)
			}
			if err := templateValues(q, tpl, sets, secrets, secretEnvs); err != nil {
				return engine.WithCode(engine.ExitInvalid, err)
			}
			var try serve.TemplateTry
			raw, err := templateCall(srv, "/api/templates/try", q, &try)
			if err != nil {
				return err
			}
			refused := try.Policy != nil && try.Policy.State == "refused"
			code := engine.ExitOK
			switch {
			case try.State != "valid":
				code = engine.ExitInvalid
			case refused:
				code = engine.ExitRefused
			}
			if rt.flags.JSON {
				if err := emitJSON(raw); err != nil {
					return err
				}
			} else {
				printTry(rt, &try)
			}
			if code != engine.ExitOK {
				return engine.WithCode(code, errSilent)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "the name of the new instance (letters, digits, dashes and dots)")
	cmd.Flags().StringVar(&at, "at", "", "the struct the instance goes into (default: the top level)")
	cmd.Flags().StringArrayVar(&sets, "set", nil, "INPUT=VALUE, for an input that is not a secret; repeatable")
	cmd.Flags().StringArrayVar(&secrets, "secret", nil, "INPUT=NAME, a secret input read from a secret of the state; repeatable")
	cmd.Flags().StringArrayVar(&secretEnvs, "secret-env", nil, "INPUT=NAME, a secret input read from an environment variable; repeatable")
	_ = cmd.MarkFlagRequired("name")
	addTagFlags(rt, cmd)
	addAccessDirFlag(rt, cmd)
	return cmd
}

// templateValues puts the --set and secret flags into the query of the wizard: a value is the CUE of the input.
func templateValues(q url.Values, tpl *serve.TemplateEntry, sets, secrets, secretEnvs []string) error {
	kind := map[string]serve.TemplateInput{}
	for _, in := range tpl.Inputs {
		kind[in.Name] = in
	}
	split := func(flag, kv string) (string, string, error) {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return "", "", fmt.Errorf("--%s wants INPUT=VALUE, not %q", flag, kv)
		}
		return k, v, nil
	}
	for _, kv := range sets {
		k, v, err := split("set", kv)
		if err != nil {
			return err
		}
		in, ok := kind[k]
		if !ok {
			return fmt.Errorf("template %s has no input %q", tpl.Name, k)
		}
		q.Set("v."+k, cueOfFlag(in, v))
	}
	for _, kv := range secrets {
		k, v, err := split("secret", kv)
		if err != nil {
			return err
		}
		q.Set("s."+k, v)
	}
	for _, kv := range secretEnvs {
		k, v, err := split("secret-env", kv)
		if err != nil {
			return err
		}
		q.Set("s."+k, v)
		q.Set("sk."+k, "env")
	}
	return nil
}

// cueOfFlag reads the value of --set as the CUE of the input: the text of a string, an option of an enum as it is
// written or as text, and CUE for anything else.
func cueOfFlag(in serve.TemplateInput, v string) string {
	quoted := strings.HasPrefix(v, `"`) || strings.HasPrefix(v, "`")
	switch in.Kind {
	case "string":
		if !quoted {
			return strconv.Quote(v)
		}
	case "enum":
		for _, o := range in.Options {
			if o == strconv.Quote(v) {
				return o
			}
		}
	}
	return v
}

// printTry writes the answer of a try for a person: the CUE first, so that it can be copied, then what was found.
func printTry(rt *runContext, t *serve.TemplateTry) {
	if t.Snippet != "" && (t.State == "valid" || t.State == "incomplete") {
		rt.out.Printf("%s", t.Snippet)
		if !strings.HasSuffix(t.Snippet, "\n") {
			rt.out.Printf("\n")
		}
		rt.out.Printf("\n")
	}
	rt.out.Printf("%s: %s", t.Address, t.State)
	if t.File != "" && t.State == "valid" {
		rt.out.Printf(" (paste it in %s)", t.File)
	}
	rt.out.Printf("\n")
	for _, p := range t.Problems {
		field := p.Field
		if field == "" || field == "@label" {
			field = "instance"
		}
		rt.out.Printf("  %s: %s\n", field, p.Message)
	}
	if len(t.Creates) > 0 {
		rt.out.Printf("creates %d resources:\n", len(t.Creates))
		for _, c := range t.Creates {
			rt.out.Printf("  %s (%s)\n", c.Address, c.Type)
		}
	}
	for _, s := range t.Secrets {
		if s.Command != "" && !s.Exists {
			rt.out.Printf("set the secret before the apply: %s\n", s.Command)
		}
	}
	if t.Policy != nil {
		rt.out.Printf("policies: %s", t.Policy.State)
		if t.Policy.Note != "" {
			rt.out.Printf(" (%s)", t.Policy.Note)
		}
		rt.out.Printf("\n")
		for _, p := range t.Policy.Policies {
			rt.out.Printf("  %s: %s\n", p.Name, p.Outcome)
			for _, is := range p.Issues {
				rt.out.Printf("    %s\n", is)
			}
		}
	}
}
