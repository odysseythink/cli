package cli

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"text/template"

	"mlib.com/mrun/containers/tree/radix"
	"mlib.com/mrun/sprig"
)

// CLI contains the state necessary to run subcommands and parse the
// command line arguments.
//
// CLI also supports nested subcommands, such as "cli foo bar". To use
// nested subcommands, the key in the Commands mapping below contains the
// full subcommand. In this example, it would be "foo bar".
//
// If you use a CLI with nested subcommands, some semantics change due to
// ambiguities:
//
//   - We use longest prefix matching to find a matching subcommand. This
//     means if you register "foo bar" and the user executes "cli foo qux",
//     the "foo" command will be executed with the arg "qux". It is up to
//     you to handle these args. One option is to just return the special
//     help return code `RunResultHelp` to display help and exit.
//
//   - The help flag "-h" or "-help" will look at all args to determine
//     the help function. For example: "otto apps list -h" will show the
//     help for "apps list" but "otto apps -h" will show it for "apps".
//     In the normal CLI, only the first subcommand is used.
//
//   - The help flag will list any subcommands that a command takes
//     as well as the command's help itself. If there are no subcommands,
//     it will note this. If the CLI itself has no subcommands, this entire
//     section is omitted.
//
//   - Any parent commands that don't exist are automatically created as
//     no-op commands that just show help for other subcommands. For example,
//     if you only register "foo bar", then "foo" is automatically created.
type CLI struct {
	// Args is the list of command-line arguments received excluding
	// the name of the app. For example, if the command "./cli foo bar"
	// was invoked, then Args should be []string{"foo", "bar"}.
	Args []string

	// Commands is a mapping of subcommand names to a factory function
	// for creating that Command implementation. If there is a command
	// with a blank string "", then it will be used as the default command
	// if no subcommand is specified.
	//
	// If the key has a space in it, this will create a nested subcommand.
	// For example, if the key is "foo bar", then to access it our CLI
	// must be accessed with "./cli foo bar". See the docs for CLI for
	// notes on how this changes some other behavior of the CLI as well.
	//
	// The factory should be as cheap as possible, ideally only allocating
	// a struct. The factory may be called multiple times in the course
	// of a command execution and certain events such as help require the
	// instantiation of all commands. Expensive initialization should be
	// deferred to function calls within the interface implementation.
	Commands map[string]CommandFactory

	// HiddenCommands is a list of commands that are "hidden". Hidden
	// commands are not given to the help function callback.
	// The values in the slice should be equivalent
	// to the keys in the command map.
	HiddenCommands []string

	// Name defines the name of the CLI.
	Name string

	// Version of the CLI.
	Version string

	// HelpFunc is the function called to generate the generic help
	// text that is shown if help must be shown for the CLI that doesn't
	// pertain to a specific command.
	HelpFunc HelpFunc

	// HelpWriter is used to print help text and version when requested.
	// Defaults to os.Stderr for backwards compatibility.
	// It is recommended that you set HelpWriter to os.Stdout, and
	// ErrorWriter to os.Stderr.
	HelpWriter io.Writer

	// ErrorWriter used to output errors when a command can not be run.
	// Defaults to the value of HelpWriter for backwards compatibility.
	// It is recommended that you set HelpWriter to os.Stdout, and
	// ErrorWriter to os.Stderr.
	ErrorWriter io.Writer

	//---------------------------------------------------------------
	// Internal fields set automatically

	once           sync.Once
	commandTree    *radix.Tree
	commandNested  bool
	commandHidden  map[string]struct{}
	subcommand     string
	subcommandArgs []string
	topFlags       []string

	// These are true when special global flags are set. We can/should
	// probably use a bitset for this one day.
	isHelp    bool
	isVersion bool
}

// NewClI returns a new CLI instance with sensible defaults.
func NewCLI(app, version string) *CLI {
	return &CLI{
		Name:     app,
		Version:  version,
		HelpFunc: BasicHelpFunc(app),
	}

}

// IsHelp returns whether or not the help flag is present within the
// arguments.
func (c *CLI) IsHelp() bool {
	c.once.Do(c.init)
	return c.isHelp
}

// IsVersion returns whether or not the version flag is present within the
// arguments.
func (c *CLI) IsVersion() bool {
	c.once.Do(c.init)
	return c.isVersion
}

// Run runs the actual CLI based on the arguments given.
func (c *CLI) Run() (int, error) {
	c.once.Do(c.init)

	// Just show the version and exit if instructed.
	if c.IsVersion() && c.Version != "" {
		c.HelpWriter.Write([]byte(c.Version + "\n"))
		return 0, nil
	}

	// Just print the help when only '-h' or '--help' is passed.
	if c.IsHelp() && c.Subcommand() == "" {
		c.HelpWriter.Write([]byte(c.HelpFunc(c.helpCommands(c.Subcommand())) + "\n"))
		return 0, nil
	}

	// Attempt to get the factory function for creating the command
	// implementation. If the command is invalid or blank, it is an error.
	raw, ok := c.commandTree.Get(c.Subcommand())
	if !ok {
		c.ErrorWriter.Write([]byte(c.HelpFunc(c.helpCommands(c.subcommandParent())) + "\n"))
		return 127, nil
	}

	command, err := raw.(CommandFactory)()
	if err != nil {
		return 1, err
	}

	// If we've been instructed to just print the help, then print it
	if c.IsHelp() {
		c.commandHelp(c.HelpWriter, command)
		return 0, nil
	}

	// If there is an invalid flag, then error
	if len(c.topFlags) > 0 {
		c.ErrorWriter.Write([]byte(
			"Invalid flags before the subcommand. If these flags are for\n" +
				"the subcommand, please put them after the subcommand.\n\n"))
		c.commandHelp(c.ErrorWriter, command)
		return 1, nil
	}

	code := command.Run(c.SubcommandArgs())
	if code == RunResultHelp {
		// Requesting help
		c.commandHelp(c.ErrorWriter, command)
		return 1, nil
	}

	return code, nil
}

// Subcommand returns the subcommand that the CLI would execute. For
// example, a CLI from "--version version --help" would return a Subcommand
// of "version"
func (c *CLI) Subcommand() string {
	c.once.Do(c.init)
	return c.subcommand
}

// SubcommandArgs returns the arguments that will be passed to the
// subcommand.
func (c *CLI) SubcommandArgs() []string {
	c.once.Do(c.init)
	return c.subcommandArgs
}

// subcommandParent returns the parent of this subcommand, if there is one.
// If there isn't on, "" is returned.
func (c *CLI) subcommandParent() string {
	// Get the subcommand, if it is "" alread just return
	sub := c.Subcommand()
	if sub == "" {
		return sub
	}

	// Clear any trailing spaces and find the last space
	sub = strings.TrimRight(sub, " ")
	idx := strings.LastIndex(sub, " ")

	if idx == -1 {
		// No space means our parent is root
		return ""
	}

	return sub[:idx]
}

func (c *CLI) init() {
	if c.HelpFunc == nil {
		c.HelpFunc = BasicHelpFunc("app")

		if c.Name != "" {
			c.HelpFunc = BasicHelpFunc(c.Name)
		}
	}

	if c.HelpWriter == nil {
		c.HelpWriter = os.Stderr
	}
	if c.ErrorWriter == nil {
		c.ErrorWriter = c.HelpWriter
	}

	// Build our hidden commands
	if len(c.HiddenCommands) > 0 {
		c.commandHidden = make(map[string]struct{})
		for _, h := range c.HiddenCommands {
			c.commandHidden[h] = struct{}{}
		}
	}

	// Build our command tree
	c.commandTree = radix.New()
	c.commandNested = false
	for k, v := range c.Commands {
		k = strings.TrimSpace(k)
		c.commandTree.Insert(k, v)
		if strings.ContainsRune(k, ' ') {
			c.commandNested = true
		}
	}

	// Go through the key and fill in any missing parent commands
	if c.commandNested {
		var walkFn radix.WalkFn
		toInsert := make(map[string]struct{})
		walkFn = func(k string, raw interface{}) bool {
			idx := strings.LastIndex(k, " ")
			if idx == -1 {
				// If there is no space, just ignore top level commands
				return false
			}

			// Trim up to that space so we can get the expected parent
			k = k[:idx]
			if _, ok := c.commandTree.Get(k); ok {
				// Yay we have the parent!
				return false
			}

			// We're missing the parent, so let's insert this
			toInsert[k] = struct{}{}

			// Call the walk function recursively so we check this one too
			return walkFn(k, nil)
		}

		// Walk!
		c.commandTree.Walk(walkFn)

		// Insert any that we're missing
		for k := range toInsert {
			var f CommandFactory = func() (Command, error) {
				return &MockCommand{
					HelpText:  "This command is accessed by using one of the subcommands below.",
					RunResult: RunResultHelp,
				}, nil
			}

			c.commandTree.Insert(k, f)
		}
	}

	// Process the args
	c.processArgs()
}

func (c *CLI) commandHelp(out io.Writer, command Command) {
	// Get the template to use
	tpl := strings.TrimSpace(defaultHelpTemplate)
	if t, ok := command.(CommandHelpTemplate); ok {
		tpl = t.HelpTemplate()
	}
	if !strings.HasSuffix(tpl, "\n") {
		tpl += "\n"
	}

	// Parse it
	t, err := template.New("root").Funcs(sprig.TxtFuncMap()).Parse(tpl)
	if err != nil {
		t = template.Must(template.New("root").Parse(fmt.Sprintf(
			"Internal error! Failed to parse command help template: %s\n", err)))
	}

	// Template data
	data := map[string]interface{}{
		"Name":           c.Name,
		"SubcommandName": c.Subcommand(),
		"Help":           command.Help(),
	}

	// Build subcommand list if we have it
	var subcommandsTpl []map[string]interface{}
	if c.commandNested {
		// Get the matching keys
		subcommands := c.helpCommands(c.Subcommand())
		keys := make([]string, 0, len(subcommands))
		for k := range subcommands {
			keys = append(keys, k)
		}

		// Sort the keys
		sort.Strings(keys)

		// Figure out the padding length
		var longest int
		for _, k := range keys {
			if v := len(k); v > longest {
				longest = v
			}
		}

		// Go through and create their structures
		subcommandsTpl = make([]map[string]interface{}, 0, len(subcommands))
		for _, k := range keys {
			// Get the command
			raw, ok := subcommands[k]
			if !ok {
				c.ErrorWriter.Write([]byte(fmt.Sprintf(
					"Error getting subcommand %q", k)))
			}
			sub, err := raw()
			if err != nil {
				c.ErrorWriter.Write([]byte(fmt.Sprintf(
					"Error instantiating %q: %s", k, err)))
			}

			// Find the last space and make sure we only include that last part
			name := k
			if idx := strings.LastIndex(k, " "); idx > -1 {
				name = name[idx+1:]
			}

			subcommandsTpl = append(subcommandsTpl, map[string]interface{}{
				"Name":        name,
				"NameAligned": name + strings.Repeat(" ", longest-len(k)),
				"Help":        sub.Help(),
				"Synopsis":    sub.Synopsis(),
			})
		}
	}
	data["Subcommands"] = subcommandsTpl

	// Write
	err = t.Execute(out, data)
	if err == nil {
		return
	}

	// An error, just output...
	c.ErrorWriter.Write([]byte(fmt.Sprintf(
		"Internal error rendering help: %s", err)))
}

// helpCommands returns the subcommands for the HelpFunc argument.
// This will only contain immediate subcommands.
func (c *CLI) helpCommands(prefix string) map[string]CommandFactory {
	// If our prefix isn't empty, make sure it ends in ' '
	if prefix != "" && prefix[len(prefix)-1] != ' ' {
		prefix += " "
	}

	// Get all the subkeys of this command
	var keys []string
	c.commandTree.WalkPrefix(prefix, func(k string, raw interface{}) bool {
		// Ignore any sub-sub keys, i.e. "foo bar baz" when we want "foo bar"
		if !strings.Contains(k[len(prefix):], " ") {
			keys = append(keys, k)
		}

		return false
	})

	// For each of the keys return that in the map
	result := make(map[string]CommandFactory, len(keys))
	for _, k := range keys {
		raw, ok := c.commandTree.Get(k)
		if !ok {
			// We just got it via WalkPrefix above, so we just panic
			panic("not found: " + k)
		}

		// If this is a hidden command, don't show it
		if _, ok := c.commandHidden[k]; ok {
			continue
		}

		result[k] = raw.(CommandFactory)
	}

	return result
}

func (c *CLI) processArgs() {
	for i, arg := range c.Args {
		if arg == "--" {
			break
		}

		// Check for help flags.
		if arg == "-h" || arg == "-help" || arg == "--help" {
			c.isHelp = true
			continue
		}

		if c.subcommand == "" {
			// Check for version flags if not in a subcommand.
			if arg == "-v" || arg == "-version" || arg == "--version" {
				c.isVersion = true
				continue
			}

			if arg != "" && arg[0] == '-' {
				// Record the arg...
				c.topFlags = append(c.topFlags, arg)
			}
		}

		// If we didn't find a subcommand yet and this is the first non-flag
		// argument, then this is our subcommand.
		if c.subcommand == "" && arg != "" && arg[0] != '-' {
			c.subcommand = arg
			if c.commandNested {
				// If the command has a space in it, then it is invalid.
				// Set a blank command so that it fails.
				if strings.ContainsRune(arg, ' ') {
					c.subcommand = ""
					return
				}

				// Determine the argument we look to to end subcommands.
				// We look at all arguments until one is a flag or has a space.
				// This disallows commands like: ./cli foo "bar baz". An
				// argument with a space is always an argument. A blank
				// argument is always an argument.
				j := 0
				for k, v := range c.Args[i:] {
					if strings.ContainsRune(v, ' ') || v == "" || v[0] == '-' {
						break
					}

					j = i + k + 1
				}

				// Nested CLI, the subcommand is actually the entire
				// arg list up to a flag that is still a valid subcommand.
				searchKey := strings.Join(c.Args[i:j], " ")
				k, _, ok := c.commandTree.LongestPrefix(searchKey)
				if ok {
					// k could be a prefix that doesn't contain the full
					// command such as "foo" instead of "foobar", so we
					// need to verify that we have an entire key. To do that,
					// we look for an ending in a space or an end of string.
					reVerify := regexp.MustCompile(regexp.QuoteMeta(k) + `( |$)`)
					if reVerify.MatchString(searchKey) {
						c.subcommand = k
						i += strings.Count(k, " ")
					}
				}
			}

			// The remaining args the subcommand arguments
			c.subcommandArgs = c.Args[i+1:]
		}
	}

	// If we never found a subcommand and support a default command, then
	// switch to using that.
	if c.subcommand == "" {
		if _, ok := c.Commands[""]; ok {
			args := c.topFlags
			args = append(args, c.subcommandArgs...)
			c.topFlags = nil
			c.subcommandArgs = args
		}
	}
}

const defaultHelpTemplate = `
{{.Help}}{{if gt (len .Subcommands) 0}}

Subcommands:
{{- range $value := .Subcommands }}
    {{ $value.NameAligned }}    {{ $value.Synopsis }}{{ end }}
{{- end }}
`
