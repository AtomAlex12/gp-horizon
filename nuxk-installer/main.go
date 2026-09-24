// Command nuxk-installer puts nuxk on a Keenetic router over Entware SSH.
//
// It serves a small web form: enter the router's Entware SSH login, the
// installer surveys the router (read-only), shows what is already there and
// what is missing, and installs only the missing parts — deps, the stock
// nfqws2-keenetic package, nuxk-core with its web UI and adapter shim, a
// config with a fresh API token — then starts nuxk-core and checks it answers.
//
// Runs anywhere on the same network: the Raspberry Pi stack (deploy/pi), or a
// laptop (`nuxk-installer`, then open the printed address).
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"time"
)

func main() {
	var (
		listen  = flag.String("listen", ":4300", "address for the installer web form")
		payload = flag.String("payload", "", "use this payload dir instead of the embedded one (development)")
		code    = flag.String("code", os.Getenv("NUXK_INSTALLER_CODE"), "access code (default: random, printed below)")
		showVer = flag.Bool("version", false, "print version and exit")
		devRoot = flag.String("remote-root", "", "development only: prefix every router path (a fake router under a temp dir)")
	)
	flag.Parse()

	p, err := NewPayload(*payload)
	if err != nil {
		log.Fatalf("payload: %v", err)
	}
	if *showVer {
		fmt.Println("nuxk-installer", p.Version())
		return
	}
	if miss := p.Missing(); len(miss) > 0 {
		log.Printf("ВНИМАНИЕ: в сборке нет файлов %v — установка nuxk-core будет недоступна (соберите через make installer)", miss)
	}
	if *code == "" {
		*code = newCode()
	}

	s := &Server{Payload: p, Code: *code, Root: *devRoot}
	srv := &http.Server{Addr: *listen, Handler: s.Routes(), ReadHeaderTimeout: 10 * time.Second}

	fmt.Printf("\n  nuxk-installer %s\n", p.Version())
	for _, a := range localAddrs(*listen) {
		fmt.Printf("  откройте:      http://%s/\n", a)
	}
	fmt.Printf("  код доступа:   %s\n\n", *code)
	log.Fatal(srv.ListenAndServe())
}

// localAddrs lists the URLs the form is reachable at, for the banner.
func localAddrs(listen string) []string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return []string{listen}
	}
	if host != "" && host != "0.0.0.0" && host != "::" {
		return []string{listen}
	}
	var out []string
	ifs, _ := net.InterfaceAddrs()
	for _, a := range ifs {
		ipn, ok := a.(*net.IPNet)
		if !ok || ipn.IP.IsLoopback() || ipn.IP.To4() == nil {
			continue
		}
		out = append(out, net.JoinHostPort(ipn.IP.String(), port))
	}
	if len(out) == 0 {
		out = append(out, net.JoinHostPort("localhost", port))
	}
	return out
}
