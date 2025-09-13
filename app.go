package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"io"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

var (
	caddyFilePath  = getEnv("CADDYFILE_PATH", "/etc/caddy/Caddyfile")
	caddyContainer = getEnv("CADDY_CONTAINER", "caddy") // container NAME, not ID
	dockerSock     = getEnv("DOCKER_SOCK", "/var/run/docker.sock")
	allowedChatStr = os.Getenv("ALLOWED_CHAT_ID")
	mu             sync.Mutex
)

func getEnv(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func main() {
	token := os.Getenv("TELEGRAM_TOKEN")
	if token == "" {
		log.Fatal("TELEGRAM_TOKEN env required")
	}

	opts := []bot.Option{
		bot.WithDefaultHandler(messageHandler),
	}
	b, err := bot.New(token, opts...)
	if err != nil {
		log.Fatal(err)
	}

	log.Println("Bot started")
	ctx := context.Background()
	b.Start(ctx)
}

func messageHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update.Message == nil {
		return
	}

	allowedChatID := int64(0)
	if allowedChatStr != "" {
		_, err := fmt.Sscan(allowedChatStr, &allowedChatID)
		if err != nil {
			log.Printf("invalid ALLOWED_CHAT_ID: %v", err)
		}
		if allowedChatID != 0 && update.Message.Chat.ID != allowedChatID {
			return
		}
	}

	text := strings.TrimSpace(update.Message.Text)
	if text == "" {
		send(b, ctx, update.Message.Chat.ID, "Send an IP address or `/addip <ip>`")
		return
	}

	fields := strings.Fields(text)
	var ipStr string
	if strings.HasPrefix(fields[0], "/addip") {
		if len(fields) < 2 {
			send(b, ctx, update.Message.Chat.ID, "Usage: /addip <IP address>")
			return
		}
		ipStr = fields[1]
	} else {
		ipStr = fields[0]
	}

	if !isValidIP(ipStr) {
		send(b, ctx, update.Message.Chat.ID, fmt.Sprintf("Invalid IP address: %q", ipStr))
		return
	}

	added, err := addIPToCaddyfile(ipStr)
	if err != nil {
		send(b, ctx, update.Message.Chat.ID, fmt.Sprintf("Failed to update Caddyfile: %v", err))
		return
	}
	if !added {
		send(b, ctx, update.Message.Chat.ID, fmt.Sprintf("IP %s already present", ipStr))
		return
	}

	send(b, ctx, update.Message.Chat.ID, fmt.Sprintf("Added %s to Caddyfile — reloading...", ipStr))

	if err := reloadCaddyInContainer(dockerSock, caddyContainer); err != nil {
		send(b, ctx, update.Message.Chat.ID, fmt.Sprintf("Added IP but reload failed: %v", err))
		return
	}

	send(b, ctx, update.Message.Chat.ID, "Caddy reload successful ✅")
}

func send(b *bot.Bot, ctx context.Context, chatID int64, text string) {
	_, _ = b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   text,
	})
}

func isValidIP(s string) bool {
	ip := net.ParseIP(s)
	return ip != nil
}

func addIPToCaddyfile(newIP string) (bool, error) {
	mu.Lock()
	defer mu.Unlock()

	data, err := os.ReadFile(caddyFilePath)
	if err != nil {
		return false, fmt.Errorf("read caddyfile: %w", err)
	}

	if strings.Contains(string(data), fmt.Sprintf("not remote_ip %s", newIP)) {
		return false, nil
	}

	lines := strings.Split(string(data), "\n")
	var out []string
	changed := false
	foundMarker := false

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		out = append(out, line)

		if strings.Contains(line, "# add here") {
			foundMarker = true
			out = append(out, fmt.Sprintf("\t\t\tnot remote_ip %s", newIP))
			changed = true
		}
	}

	if !foundMarker {
		return false, errors.New("no # add here marker found in Caddyfile")
	}

	backupPath := caddyFilePath + ".bak." + time.Now().Format("20060102T150405")
	if err := os.WriteFile(backupPath, data, 0644); err != nil {
		return false, fmt.Errorf("write backup: %w", err)
	}

	newContent := strings.Join(out, "\n")
	if err := os.WriteFile(caddyFilePath, []byte(newContent), 0644); err != nil {
		return false, fmt.Errorf("write caddyfile: %w", err)
	}

	return changed, nil
}

func reloadCaddyInContainer(sockPath, containerName string) error {
	client := httpClientForUnixSocket(sockPath)

	resp, err := client.Get("http://unix/containers/json")
	if err != nil {
		return fmt.Errorf("docker list containers: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("docker list containers failed: %s", string(b))
	}

	var containers []struct {
		ID    string   `json:"Id"`
		Names []string `json:"Names"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&containers); err != nil {
		return fmt.Errorf("decode containers list: %w", err)
	}

	var containerID string
	for _, c := range containers {
		for _, n := range c.Names {
			if strings.TrimPrefix(n, "/") == containerName {
				containerID = c.ID
				break
			}
		}
		if containerID != "" {
			break
		}
	}
	if containerID == "" {
		return fmt.Errorf("container %q not found", containerName)
	}

	type createExecReq struct {
		AttachStdout bool     `json:"AttachStdout"`
		AttachStderr bool     `json:"AttachStderr"`
		Cmd          []string `json:"Cmd"`
	}
	reqBody := createExecReq{
		AttachStdout: false,
		AttachStderr: false,
		Cmd:          []string{"caddy", "reload", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile"},
	}
	body, _ := json.Marshal(reqBody)
	url := fmt.Sprintf("http://unix/containers/%s/exec", containerID)
	execResp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("docker create exec: %w", err)
	}
	defer execResp.Body.Close()
	if execResp.StatusCode >= 400 {
		b, _ := io.ReadAll(execResp.Body)
		return fmt.Errorf("docker create exec failed: %s", string(b))
	}

	var createResp struct {
		ID string `json:"Id"`
	}
	if err := json.NewDecoder(execResp.Body).Decode(&createResp); err != nil {
		return fmt.Errorf("decode create exec resp: %w", err)
	}
	if createResp.ID == "" {
		return errors.New("empty exec id")
	}

	startURL := fmt.Sprintf("http://unix/exec/%s/start", createResp.ID)
	startReq := map[string]bool{"Detach": true, "Tty": false}
	startBody, _ := json.Marshal(startReq)
	startResp, err := client.Post(startURL, "application/json", bytes.NewReader(startBody))
	if err != nil {
		return fmt.Errorf("docker start exec: %w", err)
	}
	defer startResp.Body.Close()
	if startResp.StatusCode >= 400 {
		b, _ := io.ReadAll(startResp.Body)
		return fmt.Errorf("docker start exec failed: %s", string(b))
	}

	return nil
}

func httpClientForUnixSocket(sockPath string) *http.Client {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return net.Dial("unix", sockPath)
		},
	}
	return &http.Client{Transport: tr, Timeout: 10 * time.Second}
}
