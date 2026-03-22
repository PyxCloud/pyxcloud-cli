package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

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

	var sshSession *ssh.Session
	var stdin io.WriteCloser

	// Wait for init message
	_, msg, err := conn.ReadMessage()
	if err != nil {
		log.Println("Read init error:", err)
		return
	}

	var initMsg InitPayload
	if err := json.Unmarshal(msg, &initMsg); err != nil {
		log.Println("Unmarshal init error:", err)
		return
	}

	if initMsg.Type != "init" {
		log.Println("First message must be init")
		return
	}

	signer, err := ssh.ParsePrivateKey([]byte(initMsg.PrivateKey))
	if err != nil {
		log.Println("Parse private key error:", err)
		conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","message":"Invalid private key"}`))
		return
	}

	config := &ssh.ClientConfig{
		User: initMsg.User,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // For simplicity in bridge
	}

	client, err := ssh.Dial("tcp", initMsg.Host+":22", config)
	if err != nil {
		log.Println("SSH dial error:", err)
		conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"type":"error","message":"%s"}`, err.Error())))
		return
	}
	defer client.Close()

	sshSession, err = client.NewSession()
	if err != nil {
		log.Println("SSH session error:", err)
		return
	}
	defer sshSession.Close()

	stdin, err = sshSession.StdinPipe()
	if err != nil {
		log.Println("Stdin pipe error:", err)
		return
	}

	stdout, err := sshSession.StdoutPipe()
	if err != nil {
		log.Println("Stdout pipe error:", err)
		return
	}

	stderr, err := sshSession.StderrPipe()
	if err != nil {
		log.Println("Stderr pipe error:", err)
		return
	}

	// Request pseudo terminal
	modes := ssh.TerminalModes{
		ssh.ECHO:          1,     // enable echoing
		ssh.TTY_OP_ISPEED: 14400, // input speed = 14.4kbaud
		ssh.TTY_OP_OSPEED: 14400, // output speed = 14.4kbaud
	}

	if err := sshSession.RequestPty("xterm-256color", 80, 40, modes); err != nil {
		log.Println("Request pty error:", err)
		return
	}

	if err := sshSession.Shell(); err != nil {
		log.Println("Shell error:", err)
		return
	}

	// Send ready
	conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"ready"}`))

	// Forward SSH output to WebSocket
	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := stdout.Read(buf)
			if n > 0 {
				data, _ := json.Marshal(map[string]string{
					"type": "data",
					"data": string(buf[:n]),
				})
				conn.WriteMessage(websocket.TextMessage, data)
			}
			if err != nil {
				break
			}
		}
	}()

	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := stderr.Read(buf)
			if n > 0 {
				data, _ := json.Marshal(map[string]string{
					"type": "data",
					"data": string(buf[:n]),
				})
				conn.WriteMessage(websocket.TextMessage, data)
			}
			if err != nil {
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
