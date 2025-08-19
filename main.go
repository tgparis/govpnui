// GoVPNUI - StrongSwan Web UI
// by Trent Paris
// This file contains the HTTP and VICI control logic used by the GoVPNUI project. 
// GoVPNUI exposes a small set of JSON and text endpoints that allows the web UI to list connections and statistics.

package main

import (
        "encoding/json"
        "log"
        "net/http"
        "os/exec"
        "regexp"
        "sort"
        "strings"

        vici "github.com/strongswan/govici/vici"
)

func main() {
        // Control endpoints (VICI)
        http.HandleFunc("/initiate", initiateHandler)
        http.HandleFunc("/terminate", terminateHandler)

        // Text endpoints (for debugging/visibility)
        http.HandleFunc("/status_txt", statusTxtHandler)
        http.HandleFunc("/connections_txt", connectionsTxtHandler)

        // JSON endpoints used by the frontend
        http.HandleFunc("/children_json", childrenJSONHandler)            // sorted list of child names
        http.HandleFunc("/active_children_json", activeChildrenJSONHandler) // authoritative active list from --list-sas
        http.HandleFunc("/status_json", statusJSONHandler)                // per-child stats from --list-sas

        // Debug endpoint: shows which lines matched "active" rules
        http.HandleFunc("/debug_active_lines", debugActiveLinesHandler)

        // Serve the frontend (index.html, etc.) from ./static
        http.Handle("/", http.FileServer(http.Dir("./static")))

        log.Println("VPN UI backend listening on :8080")
        log.Fatal(http.ListenAndServe(":8080", nil))
}

//
// ----------- VICI control (initiate/terminate) -----------
//

func viciSession() (*vici.Session, error) {
        // Defaults to /var/run/charon.vici
        return vici.NewSession()
}

func viciInitiate(childName string) error {
        sess, err := viciSession()
        if err != nil {
                return err
        }
        defer sess.Close()

        msg := vici.NewMessage()
        child := vici.NewMessage()
        child.Set(childName, vici.NewMessage())
        msg.Set("child", child)

        _, err = sess.CommandRequest("initiate", msg)
        return err
}

func viciTerminate(childName string) error {
        sess, err := viciSession()
        if err != nil {
                return err
        }
        defer sess.Close()

        msg := vici.NewMessage()
        child := vici.NewMessage()
        child.Set(childName, vici.NewMessage())
        msg.Set("child", child)

        _, err = sess.CommandRequest("terminate", msg)
        return err
}

func initiateHandler(w http.ResponseWriter, r *http.Request) {
        name := r.URL.Query().Get("name")
        if name == "" {
                http.Error(w, "missing query parameter: name", http.StatusBadRequest)
                return
        }
        if err := viciInitiate(name); err != nil {
                http.Error(w, "initiate failed: "+err.Error(), http.StatusInternalServerError)
                return
        }
        w.Write([]byte("ok\n"))
}

func terminateHandler(w http.ResponseWriter, r *http.Request) {
        name := r.URL.Query().Get("name")
        if name == "" {
                http.Error(w, "missing query parameter: name", http.StatusBadRequest)
                return
        }
        if err := viciTerminate(name); err != nil {
                http.Error(w, "terminate failed: "+err.Error(), http.StatusInternalServerError)
                return
        }
        w.Write([]byte("ok\n"))
}

//
// ----------- Shell out helper -----------
//

func runCmd(name string, args ...string) ([]byte, error) {
        cmd := exec.Command(name, args...)
        return cmd.CombinedOutput()
}

//
// ----------- Text endpoints -----------
//

func statusTxtHandler(w http.ResponseWriter, r *http.Request) {
        out, err := runCmd("swanctl", "--list-sas")
        if err != nil {
                http.Error(w, string(out), http.StatusInternalServerError)
                return
        }
        w.Header().Set("Content-Type", "text/plain; charset=utf-8")
        w.Write(out)
}

func connectionsTxtHandler(w http.ResponseWriter, r *http.Request) {
        out, err := runCmd("swanctl", "--list-conns")
        if err != nil {
                http.Error(w, string(out), http.StatusInternalServerError)
                return
        }
        w.Header().Set("Content-Type", "text/plain; charset=utf-8")
        w.Write(out)
}

//
// ----------- /children_json (sorted) -----------
//

func childrenJSONHandler(w http.ResponseWriter, r *http.Request) {
        // Prefer --list-conns (clear child listing)
        connsOut, _ := runCmd("swanctl", "--list-conns")
        names := parseChildrenFromListConns(string(connsOut))

        // Fallback to --list-sas if needed
        if len(names) == 0 {
                sasOut, _ := runCmd("swanctl", "--list-sas")
                names = parseChildrenFromListSas(string(sasOut))
        }

        // Dedup + sort
        uniq := make(map[string]struct{})
        for _, n := range names {
                n = strings.TrimSpace(n)
                if n != "" {
                        uniq[n] = struct{}{}
                }
        }
        out := make([]string, 0, len(uniq))
        for n := range uniq {
                out = append(out, n)
        }
        sort.Strings(out)

        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode(out)
}

// Typical from --list-conns for you: "  name: TUNNEL, ..."
func parseChildrenFromListConns(text string) []string {
        var names []string

        // Lines like "  <name>: TUNNEL, ..."
        reChildTunnel := regexp.MustCompile(`(?m)^\s+([A-Za-z0-9._:-]+):\s+TUNNEL\b`)
        for _, m := range reChildTunnel.FindAllStringSubmatch(text, -1) {
                if len(m) > 1 {
                        names = append(names, m[1])
                }
        }

        // Backup: "children: a, b, c"
        reChildren := regexp.MustCompile(`(?i)\bchildren:\s*(.+)$`)
        for _, line := range strings.Split(text, "\n") {
                if m := reChildren.FindStringSubmatch(line); m != nil {
                        for _, tok := range regexp.MustCompile(`[,\s]+`).Split(m[1], -1) {
                                tok = strings.TrimSpace(tok)
                                if tok != "" {
                                        names = append(names, tok)
                                }
                        }
                }
        }

        // Backup: "  child <name>"
        reChildLine := regexp.MustCompile(`(?i)^\s*child\s+([A-Za-z0-9._:-]+)\b`)
        for _, line := range strings.Split(text, "\n") {
                if m := reChildLine.FindStringSubmatch(line); m != nil {
                        names = append(names, m[1])
                }
        }

        return names
}

// Fallback discovery from --list-sas (various formats):
//   "  name{1}:", "child name{1}:", "CHILD_SA name{1}", "child 'name'"
func parseChildrenFromListSas(text string) []string {
        var names []string
        reBrace := regexp.MustCompile(`(?m)^\s*(?:child\s+)?([A-Za-z0-9._:-]+)\{\d+\}:?`)
        for _, m := range reBrace.FindAllStringSubmatch(text, -1) {
                if len(m) > 1 {
                        names = append(names, m[1])
                }
        }
        reChildSa := regexp.MustCompile(`(?m)^\s*CHILD_SA\s+([A-Za-z0-9._:-]+)\{\d+\}\b`)
        for _, m := range reChildSa.FindAllStringSubmatch(text, -1) {
                if len(m) > 1 {
                        names = append(names, m[1])
                }
        }
        reQuoted := regexp.MustCompile(`(?m)^\s*child\s+'([^']+)'`)
        for _, m := range reQuoted.FindAllStringSubmatch(text, -1) {
                if len(m) > 1 {
                        names = append(names, m[1])
                }
        }
        return names
}

//
// ----------- /active_children_json -----------
//

func activeChildrenJSONHandler(w http.ResponseWriter, r *http.Request) {
        out, _ := runCmd("swanctl", "--list-sas")
        active := parseActiveChildrenFromListSas(string(out))

        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode(active)
}

// Detect active CHILD_SAs across many formats.
// Supports your format: "  <child>: #12345, reqid ..., INSTALLED, TUNNEL, ESP:..."
func parseActiveChildrenFromListSas(text string) []string {
        names := make(map[string]struct{})
        lines := strings.Split(text, "\n")

        // Already-supported styles
        reBrace := regexp.MustCompile(`(?i)^\s*(?:child\s+)?([A-Za-z0-9._:-]+)\{\d+\}:`)
        reChildSa := regexp.MustCompile(`(?i)^\s*CHILD_SA\s+([A-Za-z0-9._:-]+)\{\d+\}\b`)
        reQuoted := regexp.MustCompile(`(?i)^\s*child\s+'([^']+)'`)
        reInstalled := regexp.MustCompile(`(?i)^\s*(?:child\s+)?([A-Za-z0-9._:-]+):\s+INSTALLED\b`)
        reInstalledLog := regexp.MustCompile(`(?i)\binstalled\s+CHILD_SA\s+'([^']+)'`)
        reTolerantHeader := regexp.MustCompile(`(?i)^\s*(?:child\s+)?([A-Za-z0-9._:-]+):\s*(?:TUNNEL|ESP|ROUTED|ESTABLISHED)\b`)

        // NEW: "<child>: #<num>, ... ESP:" — your sample format
        reChildHashESP := regexp.MustCompile(`(?i)^\s*([A-Za-z0-9._:-]+):\s*#\d+,\s.*\bESP:`)

        for _, line := range lines {
                switch {
                case reBrace.MatchString(line):
                        names[reBrace.FindStringSubmatch(line)[1]] = struct{}{}
                case reChildSa.MatchString(line):
                        names[reChildSa.FindStringSubmatch(line)[1]] = struct{}{}
                case reQuoted.MatchString(line):
                        names[reQuoted.FindStringSubmatch(line)[1]] = struct{}{}
                case reInstalled.MatchString(line):
                        names[reInstalled.FindStringSubmatch(line)[1]] = struct{}{}
                case reInstalledLog.MatchString(line):
                        names[reInstalledLog.FindStringSubmatch(line)[1]] = struct{}{}
                case reChildHashESP.MatchString(line): // your format
                        names[reChildHashESP.FindStringSubmatch(line)[1]] = struct{}{}
                case reTolerantHeader.MatchString(line):
                        names[reTolerantHeader.FindStringSubmatch(line)[1]] = struct{}{}
                }
        }

        out := make([]string, 0, len(names))
        for n := range names {
                out = append(out, n)
        }
        sort.Strings(out)
        return out
}

//
// ----------- /status_json (stats per child) -----------
//

type ChildStats struct {
        Active   bool  `json:"active"`
        InBytes  int64 `json:"in_bytes"`
        OutBytes int64 `json:"out_bytes"`
        InPkts   int64 `json:"in_pkts"`
        OutPkts  int64 `json:"out_pkts"`
}

func statusJSONHandler(w http.ResponseWriter, r *http.Request) {
        out, err := runCmd("swanctl", "--list-sas")
        if err != nil {
                http.Error(w, string(out), http.StatusInternalServerError)
                return
        }
        stats := parseStatusToStats(string(out))

        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode(stats)
}

// Parse per-child stats from --list-sas.
// Supports headers like "<child>: #<num>, ... ESP:..." and classic "{n}" styles.
// Supports traffic lines like:
//   "in  <hexSPI>, 123 bytes, 4 packets"
//   "out <hexSPI>, 456 bytes, 7 packets"
// and also keeps several legacy variants.
func parseStatusToStats(text string) map[string]ChildStats {
        stats := make(map[string]ChildStats)
        lines := strings.Split(text, "\n")

        // Headers
        reHeaderBrace := regexp.MustCompile(`^\s*(?:child\s+)?([A-Za-z0-9._:-]+)\{\d+\}:?`)
        reHeaderChildSA := regexp.MustCompile(`^\s*CHILD_SA\s+([A-Za-z0-9._:-]+)\{\d+\}\b`)
        reHeaderQuoted := regexp.MustCompile(`^\s*child\s+'([^']+)'`)
        // NEW: "<child>: #<num>, ... ESP:..."
        reHeaderHashESP := regexp.MustCompile(`^\s*([A-Za-z0-9._:-]+):\s*#\d+,\s.*\bESP:`)

        // Traffic counters
        reInPrimary := regexp.MustCompile(`^\s*in:\s*([\d,]+)\s*bytes,\s*([\d,]+)\s*packets`)
        reOutPrimary := regexp.MustCompile(`^\s*out:\s*([\d,]+)\s*bytes,\s*([\d,]+)\s*packets`)
        reInAlt := regexp.MustCompile(`^\s*in:.*?\bbytes\s+([\d,]+).*?\bpackets\s+([\d,]+)`)
        reOutAlt := regexp.MustCompile(`^\s*out:.*?\bbytes\s+([\d,]+).*?\bpackets\s+([\d,]+)`)
        // Your sample: "in  <hexSPI>,      0 bytes,     0 packets"
        reInSPI := regexp.MustCompile(`^\s*in\s+[0-9A-Fa-fx]+,\s*([\d,]+)\s*bytes,\s*([\d,]+)\s*packets`)
        reOutSPI := regexp.MustCompile(`^\s*out\s+[0-9A-Fa-fx]+,\s*([\d,]+)\s*bytes,\s*([\d,]+)\s*packets`)
        // Very loose (fallback)
        reInNoColon := regexp.MustCompile(`^\s*in\b.*?\bbytes\s+([\d,]+).*?\bpackets\s+([\d,]+)`)
        reOutNoColon := regexp.MustCompile(`^\s*out\b.*?\bbytes\s+([\d,]+).*?\bpackets\s+([\d,]+)`)

        var current string

        for _, line := range lines {
                switch {
                case reHeaderHashESP.MatchString(line): // your format first
                        current = reHeaderHashESP.FindStringSubmatch(line)[1]
                        st := stats[current]
                        st.Active = true
                        stats[current] = st
                        continue
                case reHeaderBrace.MatchString(line):
                        current = reHeaderBrace.FindStringSubmatch(line)[1]
                        st := stats[current]
                        st.Active = true
                        stats[current] = st
                        continue
                case reHeaderChildSA.MatchString(line):
                        current = reHeaderChildSA.FindStringSubmatch(line)[1]
                        st := stats[current]
                        st.Active = true
                        stats[current] = st
                        continue
                case reHeaderQuoted.MatchString(line):
                        current = reHeaderQuoted.FindStringSubmatch(line)[1]
                        st := stats[current]
                        st.Active = true
                        stats[current] = st
                        continue
                }

                if current == "" {
                        continue
                }

                switch {
                case reInSPI.MatchString(line):
                        m := reInSPI.FindStringSubmatch(line)
                        st := stats[current]
                        st.InBytes = parseInt64(stripCommas(m[1]))
                        st.InPkts = parseInt64(stripCommas(m[2]))
                        stats[current] = st
                case reOutSPI.MatchString(line):
                        m := reOutSPI.FindStringSubmatch(line)
                        st := stats[current]
                        st.OutBytes = parseInt64(stripCommas(m[1]))
                        st.OutPkts = parseInt64(stripCommas(m[2]))
                        stats[current] = st
                case reInPrimary.MatchString(line):
                        m := reInPrimary.FindStringSubmatch(line)
                        st := stats[current]
                        st.InBytes = parseInt64(stripCommas(m[1]))
                        st.InPkts = parseInt64(stripCommas(m[2]))
                        stats[current] = st
                case reOutPrimary.MatchString(line):
                        m := reOutPrimary.FindStringSubmatch(line)
                        st := stats[current]
                        st.OutBytes = parseInt64(stripCommas(m[1]))
                        st.OutPkts = parseInt64(stripCommas(m[2]))
                        stats[current] = st
                case reInAlt.MatchString(line):
                        m := reInAlt.FindStringSubmatch(line)
                        st := stats[current]
                        st.InBytes = parseInt64(stripCommas(m[1]))
                        st.InPkts = parseInt64(stripCommas(m[2]))
                        stats[current] = st
                case reOutAlt.MatchString(line):
                        m := reOutAlt.FindStringSubmatch(line)
                        st := stats[current]
                        st.OutBytes = parseInt64(stripCommas(m[1]))
                        st.OutPkts = parseInt64(stripCommas(m[2]))
                        stats[current] = st
                case reInNoColon.MatchString(line):
                        m := reInNoColon.FindStringSubmatch(line)
                        st := stats[current]
                        st.InBytes = parseInt64(stripCommas(m[1]))
                        st.InPkts = parseInt64(stripCommas(m[2]))
                        stats[current] = st
                case reOutNoColon.MatchString(line):
                        m := reOutNoColon.FindStringSubmatch(line)
                        st := stats[current]
                        st.OutBytes = parseInt64(stripCommas(m[1]))
                        st.OutPkts = parseInt64(stripCommas(m[2]))
                        stats[current] = st
                }
        }
        return stats
}

func stripCommas(s string) string {
        return strings.ReplaceAll(s, ",", "")
}

func parseInt64(s string) int64 {
        var n int64
        for i := 0; i < len(s); i++ {
                c := s[i]
                if c < '0' || c > '9' {
                        return n
                }
                n = n*10 + int64(c-'0')
        }
        return n
}

//
// ----------- /debug_active_lines -----------
//

func debugActiveLinesHandler(w http.ResponseWriter, r *http.Request) {
        out, _ := runCmd("swanctl", "--list-sas")
        text := string(out)

        reList := []*regexp.Regexp{
                // NEW: "<child>: #<num>, ... ESP: ..."
                regexp.MustCompile(`(?i)^\s*([A-Za-z0-9._:-]+):\s*#\d+,\s.*\bESP:`),
                regexp.MustCompile(`(?i)^\s*(?:child\s+)?([A-Za-z0-9._:-]+)\{\d+\}:`),
                regexp.MustCompile(`(?i)^\s*CHILD_SA\s+([A-Za-z0-9._:-]+)\{\d+\}\b`),
                regexp.MustCompile(`(?i)^\s*child\s+'([^']+)'`),
                regexp.MustCompile(`(?i)^\s*(?:child\s+)?([A-Za-z0-9._:-]+):\s+INSTALLED\b`),
                regexp.MustCompile(`(?i)\binstalled\s+CHILD_SA\s+'([^']+)'`),
                regexp.MustCompile(`(?i)^\s*(?:child\s+)?([A-Za-z0-9._:-]+):\s*(?:TUNNEL|ESP|ROUTED|ESTABLISHED)\b`),
        }

        var matched []string
        for _, line := range strings.Split(text, "\n") {
                for _, re := range reList {
                        if re.MatchString(line) {
                                matched = append(matched, line)
                                break
                        }
                }
        }

        w.Header().Set("Content-Type", "text/plain; charset=utf-8")
        if len(matched) == 0 {
                w.Write([]byte("No lines matched active-child patterns.\n\nFull output:\n" + text))
                return
        }
        w.Write([]byte(strings.Join(matched, "\n")))
}
