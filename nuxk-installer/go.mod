module nuxk.dev/horizon/installer

go 1.24.0

// x/crypto/ssh is the one dependency: the installer is an SSH client. It is a
// separate module so nuxk-core (which runs on the router) stays stdlib-only.
require golang.org/x/crypto v0.44.0

require golang.org/x/sys v0.38.0 // indirect
