package tui

// fieldKind is how a form field is edited.
type fieldKind int

const (
	kindText   fieldKind = iota // free text
	kindChoice                  // a few fixed values, shown inline
	kindList                    // vertical list, optionally with a typed entry
	kindToggle                  // expands or collapses the advanced fields
)

// fieldDef describes a form field. Fields whose id is an ssh directive are
// written as that directive; an empty value writes nothing.
type fieldDef struct {
	id          string
	label       string // defaults to id
	section     string
	kind        fieldKind
	choices     []string // kindChoice, "unset" is added first
	placeholder string
	numeric     bool
	advanced    bool // hidden until "Advanced options" is expanded
	help        string
}

func (d fieldDef) title() string {
	if d.label != "" {
		return d.label
	}
	return d.id
}

// Form ids that are not ssh directives.
const (
	idAlias    = "alias"
	idDest     = "dest"
	idFile     = "file"
	idAdvanced = "advanced"
)

var yesNo = []string{"yes", "no"}

var fieldDefs = []fieldDef{
	{id: idAlias, label: "Alias (Host)", section: "Host", kind: kindText, placeholder: "my-server",
		help: "The name you type after ssh (ssh my-server). Several space-separated names are allowed. Wildcards (* ?) turn it into a pattern whose options apply to the matching Hosts."},

	{id: "HostName", section: "Connection", kind: kindText, placeholder: "10.0.0.1 or DNS name",
		help: "The real address to connect to: an IP or a DNS name. Empty: the alias itself is used as the address."},

	{id: "User", section: "Connection", kind: kindText, placeholder: "ubuntu",
		help: "The login on the server. Empty: your local user name."},

	{id: "Port", section: "Connection", kind: kindText, placeholder: "22", numeric: true,
		help: "The server's ssh port. Empty: 22."},

	{id: "ProxyJump", section: "Connection", kind: kindText, placeholder: "bastion",
		help: "Hosts to go through before reaching this one, like ssh -J. Comma-separated for several hops; the aliases defined here work. → completes a known Host."},

	{id: "IdentityFile", section: "Authentication", kind: kindList,
		help: "The private key used to log in. none: ssh tries its default keys (~/.ssh/id_*) and those in your agent. custom path: any key file, ~ being your home."},

	{id: idDest, label: "Destination", section: "Storage", kind: kindList,
		help: "Where the Host is saved. local: your personal overrides, never shared, read first. A repository: shared with your team, pushed on save. ~/.ssh/config: this machine only."},

	{id: idFile, label: "File", section: "Storage", kind: kindList,
		help: "The repository file that holds the Host. Hosts are grouped by prefix (fairfair-live.conf). new file: create one; typing on the list starts a new file too (except h, which opens this help)."},

	{id: idAdvanced, label: "Advanced options", section: "Advanced", kind: kindToggle,
		help: "Every other ssh option: proxies, host keys, forwarding, keep-alive, multiplexing. Collapsed when the form opens; enter, space or → opens it, ← closes it. The count shows how many of these options the Host already uses."},

	{advanced: true, id: "ProxyCommand", section: "Connection", kind: kindText, placeholder: "ssh -W %h:%p bastion",
		help: "A command whose input and output carry the connection, for setups ProxyJump cannot express. %h, %p and %r stand for host, port and user. Prefer ProxyJump when it is enough."},

	{advanced: true, id: "ConnectTimeout", section: "Connection", kind: kindText, placeholder: "10", numeric: true,
		help: "Seconds to wait for the server before giving up. Empty: the system timeout, which can be minutes."},

	{advanced: true, id: "IdentitiesOnly", section: "Authentication", kind: kindChoice, choices: yesNo,
		help: "yes: offer only the IdentityFile above, not every key in your agent. Avoids \"Too many authentication failures\" when the agent holds many keys."},

	{advanced: true, id: "PubkeyAuthentication", section: "Authentication", kind: kindChoice, choices: yesNo,
		help: "Whether to log in with a key. Default: yes."},

	{advanced: true, id: "PasswordAuthentication", section: "Authentication", kind: kindChoice, choices: yesNo,
		help: "Whether to allow logging in with a password. Default: yes."},

	{advanced: true, id: "AddKeysToAgent", section: "Authentication", kind: kindChoice, choices: []string{"yes", "no", "ask", "confirm"},
		help: "Add the key to your ssh-agent after its first use, so its passphrase is asked once. ask: confirm before adding it. confirm: confirm every use."},

	{advanced: true, id: "StrictHostKeyChecking", section: "Authentication", kind: kindChoice, choices: []string{"yes", "accept-new", "no", "ask"},
		help: "What to do with an unknown or changed server key. yes: refuse unknown servers. accept-new: trust new servers, refuse changed keys. no: accept everything (unsafe). ask (default): prompt."},

	{advanced: true, id: "UserKnownHostsFile", section: "Authentication", kind: kindText, placeholder: "~/.ssh/known_hosts",
		help: "Where server keys are remembered. /dev/null forgets them, for throwaway machines (with StrictHostKeyChecking no)."},

	{advanced: true, id: "ForwardAgent", section: "Forwarding", kind: kindChoice, choices: yesNo,
		help: "Make your local ssh-agent usable from the server, e.g. for git over ssh there. Only for servers you trust: their admins can use your keys while you are connected."},

	{advanced: true, id: "LocalForward", section: "Forwarding", kind: kindText, placeholder: "5432 localhost:5432",
		help: "Opens a port on your machine that reaches an address through the server: \"local-port host:port\". 5432 localhost:5432 reaches the server's PostgreSQL on your port 5432."},

	{advanced: true, id: "RemoteForward", section: "Forwarding", kind: kindText, placeholder: "8080 localhost:3000",
		help: "The opposite of LocalForward: a port on the server reaches an address on your side: \"remote-port host:port\"."},

	{advanced: true, id: "DynamicForward", section: "Forwarding", kind: kindText, placeholder: "1080", numeric: true,
		help: "A local SOCKS proxy port: the applications that use it go out through the server."},

	{advanced: true, id: "ForwardX11", section: "Forwarding", kind: kindChoice, choices: yesNo,
		help: "Show the server's graphical applications on your screen (X11). Default: no."},

	{advanced: true, id: "ServerAliveInterval", section: "Session", kind: kindText, placeholder: "60", numeric: true,
		help: "Send a keep-alive after N seconds of silence, so NATs and firewalls do not cut idle sessions. Empty or 0: never."},

	{advanced: true, id: "ServerAliveCountMax", section: "Session", kind: kindText, placeholder: "3", numeric: true,
		help: "Unanswered keep-alives before disconnecting. With ServerAliveInterval 60 and 3, a dead connection is noticed after 3 minutes."},

	{advanced: true, id: "Compression", section: "Session", kind: kindChoice, choices: yesNo,
		help: "Compress the traffic. Helps on slow links, costs CPU on fast ones. Default: no."},

	{advanced: true, id: "RequestTTY", section: "Session", kind: kindChoice, choices: []string{"yes", "no", "force", "auto"},
		help: "Whether to open a terminal on the server. force: always, needed when RemoteCommand runs something interactive (sudo -i, tmux)."},

	{advanced: true, id: "RemoteCommand", section: "Session", kind: kindText, placeholder: "tmux attach || tmux",
		help: "A command run on the server instead of your login shell."},

	{advanced: true, id: "SetEnv", section: "Session", kind: kindText, placeholder: "LANG=C.UTF-8",
		help: "Environment variables set on the server, as NAME=value separated by spaces. The server must accept them (AcceptEnv)."},

	{advanced: true, id: "ControlMaster", section: "Multiplexing", kind: kindChoice, choices: []string{"yes", "no", "auto", "ask", "autoask"},
		help: "Share one connection between several ssh sessions: the next ones open instantly. auto: create the shared connection when there is none. Needs ControlPath."},

	{advanced: true, id: "ControlPath", section: "Multiplexing", kind: kindText, placeholder: "~/.ssh/cm-%r@%h:%p",
		help: "The socket of the shared connection. %r, %h and %p (user, host, port) give one socket per destination."},

	{advanced: true, id: "ControlPersist", section: "Multiplexing", kind: kindText, placeholder: "10m",
		help: "Keep the shared connection open this long after the last session closes: seconds, or 10m, 1h. yes: until killed."},
}
