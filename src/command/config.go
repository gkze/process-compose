package command

type ShellConfig struct {
	ShellCommand     string `yaml:"shell_command" json:"shellCommand,omitempty"`
	ShellArgument    string `yaml:"shell_argument" json:"shellArgument,omitempty"`
	ElevatedShellCmd string `yaml:"elevated_shell_command,omitempty" json:"elevatedShellCmd,omitempty"`
	ElevatedShellArg string `yaml:"elevated_shell_argument,omitempty" json:"elevatedShellArg,omitempty"`
}
