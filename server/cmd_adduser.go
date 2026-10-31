package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"golang.org/x/term"
)

// runAddUser implements:  server adduser [-db path] <username> [password]
// If the password is omitted it is read from the terminal without echo.
func runAddUser(args []string) {
	fs := flag.NewFlagSet("adduser", flag.ExitOnError)
	dbPath := fs.String("db", envOr("NYK_DB", "nyk.db"), "path to the SQLite database file")
	_ = fs.Parse(args)

	rest := fs.Args()
	if len(rest) < 1 {
		fmt.Fprintln(os.Stderr, "usage: server adduser [-db path] <username> [password]")
		os.Exit(2)
	}
	username := rest[0]

	var password string
	if len(rest) >= 2 {
		password = rest[1]
	} else {
		password = promptPassword()
	}
	if strings.TrimSpace(username) == "" || password == "" {
		log.Fatal("username and password must not be empty")
	}

	store, err := OpenStore(*dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer store.Close()

	if err := store.CreateTech(username, password); err != nil {
		log.Fatalf("create tech: %v", err)
	}
	fmt.Printf("technician %q created/updated in %s\n", username, *dbPath)
}

func promptPassword() string {
	fmt.Print("Password: ")
	if term.IsTerminal(int(os.Stdin.Fd())) {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			log.Fatalf("read password: %v", err)
		}
		return string(b)
	}
	// Non-interactive (e.g. piped): read a line.
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimRight(line, "\r\n")
}
