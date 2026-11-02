package openvpn

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sentinel-official/sentinel-go-sdk/v3/service"
)

const (
	commandTimeout = 10 * time.Second
	connectedState = "CONNECTED,SUCCESS"
)

var (
	errManagementCommandExpired = errors.New("management command timed out")
	errManagementCommandFailed  = errors.New("management command failed")
	errManagementNotAttached    = errors.New("management connection is not attached")
	errTunnelNotConnected       = errors.New("tunnel not connected")
)

type management struct {
	cids        map[string]map[string]bool
	credentials map[string]string
	mu          sync.RWMutex
	qmu         sync.Mutex
	queue       []chan string
	w           io.Writer
}

func newManagement(peers map[string]string) *management {
	return &management{
		cids:        map[string]map[string]bool{},
		credentials: peers,
	}
}

func (m *management) allow(user, password string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	want, ok := m.credentials[user]

	return ok && want == password
}

func (m *management) attach(w io.Writer) {
	m.qmu.Lock()
	defer m.qmu.Unlock()

	m.w = w
}

func (m *management) deliver(reply string) {
	m.qmu.Lock()
	defer m.qmu.Unlock()

	if len(m.queue) == 0 {
		return
	}

	ch := m.queue[0]
	m.queue = m.queue[1:]

	ch <- reply
}

func (m *management) exec(cmd string) (string, error) {
	ch, err := m.send(cmd)
	if err != nil {
		return "", err
	}

	select {
	case reply := <-ch:
		if strings.HasPrefix(reply, "ERROR:") {
			return "", fmt.Errorf("%w: %s", errManagementCommandFailed, reply)
		}

		return reply, nil
	case <-time.After(commandTimeout):
		return "", fmt.Errorf("%w: %s", errManagementCommandExpired, cmd)
	}
}

func (m *management) forgetClient(cid string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, cids := range m.cids {
		delete(cids, cid)
	}
}

func (m *management) hasClient(user, cid string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.cids[user] != nil && m.cids[user][cid]
}

func (m *management) recordClient(user, cid string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cids[user] == nil {
		m.cids[user] = map[string]bool{}
	}

	m.cids[user][cid] = true
}

func (m *management) send(cmd string) (chan string, error) {
	m.qmu.Lock()
	defer m.qmu.Unlock()

	if m.w == nil {
		return nil, errManagementNotAttached
	}

	if _, err := io.WriteString(m.w, cmd+"\n"); err != nil {
		return nil, fmt.Errorf("writing %q: %w", cmd, err)
	}

	ch := make(chan string, 1)
	m.queue = append(m.queue, ch)

	return ch, nil
}

func (m *management) serve(r *bufio.Reader, w io.Writer) error {
	var (
		cid, kid string
		env      = map[string]string{}
		reply    []string
	)

	m.attach(w)
	defer m.attach(nil)

	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil
		}

		line = strings.TrimSpace(line)

		if !strings.HasPrefix(line, ">") {
			if line == "END" || strings.HasPrefix(line, "SUCCESS:") || strings.HasPrefix(line, "ERROR:") {
				reply = append(reply, line)
				m.deliver(strings.Join(reply, "\n"))
				reply = nil
			} else if line != "" {
				reply = append(reply, line)
			}

			continue
		}

		switch {
		case strings.HasPrefix(line, ">CLIENT:CONNECT,"), strings.HasPrefix(line, ">CLIENT:REAUTH,"):
			parts := strings.Split(strings.SplitN(line, ",", 2)[1], ",")
			cid, kid = parts[0], parts[1]
			env = map[string]string{}
		case strings.HasPrefix(line, ">CLIENT:DISCONNECT,"):
			m.forgetClient(strings.TrimPrefix(line, ">CLIENT:DISCONNECT,"))
		case strings.HasPrefix(line, ">CLIENT:ENV,END"):
			if cid == "" {
				continue
			}

			if m.allow(env["username"], env["password"]) {
				if _, err := m.send(fmt.Sprintf("client-auth-nt %s %s", cid, kid)); err != nil {
					return err
				}

				m.recordClient(env["username"], cid)
			} else {
				if _, err := m.send(fmt.Sprintf("client-deny %s %s \"bad credentials\"", cid, kid)); err != nil {
					return err
				}
			}

			cid, kid = "", ""
		case strings.HasPrefix(line, ">CLIENT:ENV,"):
			kv := strings.SplitN(strings.TrimPrefix(line, ">CLIENT:ENV,"), "=", 2)
			if len(kv) == 2 {
				env[kv[0]] = kv[1]
			}
		}
	}
}

func headerIndex(header []string, name string) int {
	for i, h := range header {
		if h == name {
			return i
		}
	}

	return -1
}

func parseStatus(out string) map[string]service.PeerStats {
	stats := map[string]service.PeerStats{}

	var header []string

	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.Split(line, "\t")

		switch fields[0] {
		case "HEADER":
			if len(fields) > 1 && fields[1] == "CLIENT_LIST" {
				header = fields[2:]
			}
		case "CLIENT_LIST":
			row := fields[1:]
			userIdx, rxIdx, txIdx := headerIndex(
				header,
				"Username",
			), headerIndex(header, "Bytes Received"), headerIndex(header, "Bytes Sent")

			if userIdx < 0 || rxIdx < 0 || txIdx < 0 || userIdx >= len(row) || rxIdx >= len(row) || txIdx >= len(row) {
				continue
			}

			rx, _ := strconv.ParseUint(row[rxIdx], 10, 64)
			tx, _ := strconv.ParseUint(row[txIdx], 10, 64)
			stats[row[userIdx]] = service.PeerStats{
				RxBytes: rx,
				TxBytes: tx,
			}
		}
	}

	return stats
}

func readState(r *bufio.Reader) (string, error) {
	var state string

	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return "", fmt.Errorf("reading reply: %w", err)
		}

		line = strings.TrimSpace(line)
		if line == "END" {
			return state, nil
		}

		fields := strings.Split(line, ",")
		if state == "" && !strings.HasPrefix(line, ">") && len(fields) > 2 {
			state = fields[1] + "," + fields[2]
		}
	}
}
