package passocli

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type commandsCatalog struct {
	SchemaVersion int                  `json:"schemaVersion"`
	Commands      []commandDescription `json:"commands"`
}

type commandDescription struct {
	InputSchema      json.RawMessage   `json:"inputSchema,omitempty"`
	InputExample     json.RawMessage   `json:"inputExample,omitempty"`
	BodyRequired     bool              `json:"bodyRequired,omitempty"`
	Path             string            `json:"path"`
	Use              string            `json:"use"`
	ShortDescription string            `json:"shortDescription"`
	Hidden           bool              `json:"hidden"`
	Flags            []flagDescription `json:"flags"`
}

type flagDescription struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
}

func newCommandsCatalogCommand(root *cobra.Command, asJSON *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "commands",
		Short: "List visible commands and their flags",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			catalog := buildCommandsCatalog(root)
			if asJSON != nil && *asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(catalog)
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "Commands:")
			if err != nil {
				return err
			}
			for _, command := range catalog.Commands {
				visible, _, findErr := root.Find(strings.Fields(command.Path))
				if findErr == nil && visible != nil && !visible.Runnable() && visible.HasAvailableSubCommands() {
					continue
				}
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "  %-28s %s\n", command.Path, command.ShortDescription); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

func buildCommandsCatalog(root *cobra.Command) commandsCatalog {
	commands := make([]commandDescription, 0)
	for _, child := range root.Commands() {
		appendCommandDescriptions(root, child, &commands)
	}
	sort.Slice(commands, func(i, j int) bool { return commands[i].Path < commands[j].Path })
	return commandsCatalog{SchemaVersion: 1, Commands: commands}
}

func appendCommandDescriptions(root, cmd *cobra.Command, out *[]commandDescription) {
	if cmd == nil || !cmd.IsAvailableCommand() || cmd.Name() == "help" || cmd.Name() == "completion" {
		return
	}
	path := strings.TrimSpace(strings.TrimPrefix(cmd.CommandPath(), root.CommandPath()))
	short := strings.TrimSpace(cmd.Short)
	if short == "" {
		short = strings.TrimSpace(strings.SplitN(cmd.Long, "\n", 2)[0])
	}
	if short == "" {
		short = path + " command"
	}
	*out = append(*out, commandDescription{
		Path:             path,
		Use:              strings.TrimSpace(cmd.Use),
		ShortDescription: short,
		Hidden:           false,
		Flags:            commandFlags(cmd),
		InputSchema:      json.RawMessage(cmd.Annotations["inputSchema"]),
		InputExample:     json.RawMessage(cmd.Annotations["inputExample"]),
		BodyRequired:     cmd.Annotations["bodyRequired"] == "true",
	})
	for _, child := range cmd.Commands() {
		appendCommandDescriptions(root, child, out)
	}
}

func commandFlags(cmd *cobra.Command) []flagDescription {
	flags := map[string]flagDescription{}
	// Visit the resolved inherited set and local set independently; a command
	// may override an inherited flag, in which case its local definition wins.
	if inherited := cmd.InheritedFlags(); inherited != nil {
		inherited.VisitAll(func(flag *pflag.Flag) {
			if flag.Name != "help" {
				flags[flag.Name] = describeFlag(flag)
			}
		})
	}
	if local := cmd.LocalFlags(); local != nil {
		local.VisitAll(func(flag *pflag.Flag) {
			if flag.Name != "help" {
				flags[flag.Name] = describeFlag(flag)
			}
		})
	}
	out := make([]flagDescription, 0, len(flags))
	for _, flag := range flags {
		out = append(out, flag)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func describeFlag(flag *pflag.Flag) flagDescription {
	required := false
	if annotation := flag.Annotations[cobra.BashCompOneRequiredFlag]; len(annotation) > 0 && annotation[0] == "true" {
		required = true
	}
	return flagDescription{Name: flag.Name, Type: flag.Value.Type(), Required: required}
}
