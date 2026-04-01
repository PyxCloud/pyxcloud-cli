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

	var sshSession *ssh.Session
	var stdin io.WriteCloser

	// Wait for init message
	_, msg, err := conn.ReadMessage()
	if err != nil {
		log.Println("[Local Bridge] Read init error:", err)
		return
	}
	log.Println("[Local Bridge] Received init payload")

	var initMsg InitPayload
	if err := json.Unmarshal(msg, &initMsg); err != nil {
		log.Println("[Local Bridge] Unmarshal init error:", err)
		return
	}

	if initMsg.Type != "init" {
		log.Println("[Local Bridge] First message must be init")
		return
	}

	signer, err := ssh.ParsePrivateKey([]byte(initMsg.PrivateKey))
	if err != nil {
		log.Println("[Local Bridge] Parse private key error:", err)
		conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","message":"Invalid private key"}`))
		return
	}

	// DEBUG: Log the public key derived from the private key for fingerprint matching
	pubKeyBytes := ssh.MarshalAuthorizedKey(signer.PublicKey())
	log.Printf("[Local Bridge] DEBUG: Private key parsed OK. Public key type: %s", signer.PublicKey().Type())
	log.Printf("[Local Bridge] DEBUG: Derived public key (first 120 chars): %.120s", string(pubKeyBytes))
	log.Printf("[Local Bridge] DEBUG: Private key PEM header (first 40 chars): %.40s", initMsg.PrivateKey)

	config := &ssh.ClientConfig{
		User: initMsg.User,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // For simplicity in bridge
	}

	log.Printf("[Local Bridge] Dialing SSH to %s@%s:22...\n", initMsg.User, initMsg.Host)
	client, err := ssh.Dial("tcp", initMsg.Host+":22", config)
	if err != nil {
		log.Println("[Local Bridge] SSH dial error:", err)
		conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"type":"error","message":"%s"}`, err.Error())))
		return
	}
	defer client.Close()
	log.Println("[Local Bridge] SSH Dial successful")

	sshSession, err = client.NewSession()
	if err != nil {
		log.Println("[Local Bridge] SSH session error:", err)
		return
	}
	defer sshSession.Close()

	stdin, err = sshSession.StdinPipe()
	if err != nil {
		log.Println("[Local Bridge] Stdin pipe error:", err)
		return
	}

	stdout, err := sshSession.StdoutPipe()
	if err != nil {
		log.Println("[Local Bridge] Stdout pipe error:", err)
		return
	}

	stderr, err := sshSession.StderrPipe()
	if err != nil {
		log.Println("[Local Bridge] Stderr pipe error:", err)
		return
	}

	// Request pseudo terminal
	modes := ssh.TerminalModes{
		ssh.ECHO:          1,     // enable echoing
		ssh.TTY_OP_ISPEED: 14400, // input speed = 14.4kbaud
		ssh.TTY_OP_OSPEED: 14400, // output speed = 14.4kbaud
	}

	log.Println("[Local Bridge] Requesting PTY...")
	if err := sshSession.RequestPty("xterm-256color", 80, 40, modes); err != nil {
		log.Println("[Local Bridge] Request pty error:", err)
		return
	}

	log.Println("[Local Bridge] Starting Shell...")
	if err := sshSession.Shell(); err != nil {
		log.Println("[Local Bridge] Shell error:", err)
		return
	}
	log.Println("[Local Bridge] Shell started successfully")

	// Send ready
	var writeMu sync.Mutex
	writeMsg := func(msg []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return conn.WriteMessage(websocket.TextMessage, msg)
	}

	writeMsg([]byte(`{"type":"ready"}`))

	// Forward SSH output to WebSocket
	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := stdout.Read(buf)
			if n > 0 {
				log.Printf("[Local Bridge] Read %d bytes from stdout\n", n)
				data, jerr := json.Marshal(map[string]string{
					"type": "data",
					"data": string(buf[:n]),
				})
				if jerr != nil {
					log.Println("[Local Bridge] JSON Marshal error:", jerr)
				} else {
					writeMsg(data)
				}
			}
			if err != nil {
				log.Println("[Local Bridge] stdout read error:", err)
				break
			}
		}
	}()

	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := stderr.Read(buf)
			if n > 0 {
				log.Printf("[Local Bridge] Read %d bytes from stderr\n", n)
				data, jerr := json.Marshal(map[string]string{
					"type": "data",
					"data": string(buf[:n]),
				})
				if jerr != nil {
					log.Println("[Local Bridge] JSON Marshal stderr error:", jerr)
				} else {
					writeMsg(data)
				}
			}
			if err != nil {
				log.Println("[Local Bridge] stderr read error:", err)
				break
			}
		}
	}()

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
