package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		// Allow any origin since this is a local bridge for the PyxCloud UI
		return true
	},
}

type InitPayload struct {
	Type       string `json:"type"`
	Host       string `json:"host"`
	User       string `json:"user"`
	PrivateKey string `json:"privateKey"`
}

type DataPayload struct {
	Type string `json:"type"`
	Data string `json:"data"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

func handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("Upgrade error:", err)
		return
	}
	defer conn.Close()
	log.Println("[Local Bridge] WebSocket connected")

	initMsg, err := handleWebSocketInit(conn)
	if err != nil {
		log.Println("[Local Bridge] Init error:", err)
		conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"type":"error","message":"%s"}`, err.Error())))
		return
	}

	log.Printf("[Local Bridge] Dialing SSH to %s@%s:22...\n", initMsg.User, initMsg.Host)
	client, err := setupSSHClient(initMsg)
	if err != nil {
		log.Println("[Local Bridge] SSH dial error:", err)
		conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"type":"error","message":"%s"}`, err.Error())))
		return
	}
	defer client.Close()
	log.Println("[Local Bridge] SSH Dial successful")

	sshSession, err := client.NewSession()
	if err != nil {
		log.Println("[Local Bridge] SSH session error:", err)
		return
	}
	defer sshSession.Close()

	stdin, stdout, stderr, err := setupSSHPipesAndShell(sshSession)
	if err != nil {
		log.Println("[Local Bridge] SSH setup error:", err)
		return
	}

	// Send ready
	var writeMu sync.Mutex
	writeMsg := func(msg []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return conn.WriteMessage(websocket.TextMessage, msg)
	}

	writeMsg([]byte(`{"type":"ready"}`))

	go pumpOutput(stdout, writeMsg, "stdout")
	go pumpOutput(stderr, writeMsg, "stderr")

	// Forward WebSocket input to SSH
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			break
		}

		var payload DataPayload
		if err := json.Unmarshal(msg, &payload); err == nil {
			if payload.Type == "data" {
				stdin.Write([]byte(payload.Data))
			} else if payload.Type == "resize" {
				sshSession.WindowChange(payload.Rows, payload.Cols)
			}
		}
	}
}

func setupSSHPipesAndShell(sshSession *ssh.Session) (io.WriteCloser, io.Reader, io.Reader, error) {
	stdin, err := sshSession.StdinPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("stdin pipe: %w", err)
	}

	stdout, err := sshSession.StdoutPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("stdout pipe: %w", err)
	}

	stderr, err := sshSession.StderrPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("stderr pipe: %w", err)
	}

	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}

	log.Println("[Local Bridge] Requesting PTY...")
	if err := sshSession.RequestPty("xterm-256color", 80, 40, modes); err != nil {
		return nil, nil, nil, fmt.Errorf("request pty: %w", err)
	}

	log.Println("[Local Bridge] Starting Shell...")
	if err := sshSession.Shell(); err != nil {
		return nil, nil, nil, fmt.Errorf("start shell: %w", err)
	}
	log.Println("[Local Bridge] Shell started successfully")

	return stdin, stdout, stderr, nil
}

func pumpOutput(r io.Reader, writeMsg func([]byte) error, streamName string) {
	buf := make([]byte, 1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			log.Printf("[Local Bridge] Read %d bytes from %s\n", n, streamName)
			data, jerr := json.Marshal(map[string]string{
				"type": "data",
				"data": string(buf[:n]),
			})
			if jerr != nil {
				log.Printf("[Local Bridge] JSON Marshal %s error: %v\n", streamName, jerr)
			} else {
				writeMsg(data)
			}
		}
		if err != nil {
			log.Printf("[Local Bridge] %s read error: %v\n", streamName, err)
			break
		}
	}
}

func handleWebSocketInit(conn *websocket.Conn) (InitPayload, error) {
	var initMsg InitPayload
	_, msg, err := conn.ReadMessage()
	if err != nil {
		return initMsg, fmt.Errorf("read init payload: %w", err)
	}
	if err := json.Unmarshal(msg, &initMsg); err != nil {
		return initMsg, fmt.Errorf("unmarshal init payload: %w", err)
	}
	if initMsg.Type != "init" {
		return initMsg, fmt.Errorf("first message must be 'init'")
	}
	return initMsg, nil
}

func setupSSHClient(initMsg InitPayload) (*ssh.Client, error) {
	signer, err := ssh.ParsePrivateKey([]byte(initMsg.PrivateKey))
	if err != nil {
		return nil, fmt.Errorf("invalid private key")
	}

	config := &ssh.ClientConfig{
		User: initMsg.User,
		Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	return ssh.Dial("tcp", initMsg.Host+":22", config)
}

var proxyCmd = &cobra.Command{
	Use:   "proxy",
	Short: "Starts a local HTTP/WebSocket proxy for the Cloud Shell bridge",
	Run: func(cmd *cobra.Command, args []string) {
		port, _ := cmd.Flags().GetInt("port")

		http.HandleFunc("/ws/ssh", handleWebSocket)

		// Also serve a tiny landing page so we can use an iframe if preferred
		http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			fmt.Fprintf(w, "PyxCloud Local Bridge is active. Close this window when done.")
		})

		addr := fmt.Sprintf("127.0.0.1:%d", port)
		fmt.Printf("Starting PyxCloud Local Shell Bridge on %s...\n", addr)
		fmt.Printf("✓ Secure context active. Return to your PyxCloud Dashboard.\n")

		if err := http.ListenAndServe(addr, nil); err != nil {
			log.Fatal("ListenAndServe:", err)
		}
	},
}

func init() {
	proxyCmd.Flags().IntP("port", "p", 13337, "Port to listen on for the local bridge")
	rootCmd.AddCommand(proxyCmd)
}
