package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/jkop-xyz/flowmon/internal/appstore"
	"github.com/jkop-xyz/flowmon/internal/auth"
	"github.com/jkop-xyz/flowmon/internal/config"
)

const adminUsage = `Usage: flowmon admin reset-password <username>

Sets a new password and ends the user's sessions. The password is read from the
terminal, or from standard input when it isn't a terminal:

  echo 'new password' | flowmon admin reset-password admin
`

func admin(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 2 || args[0] != "reset-password" {
		fmt.Fprint(stderr, adminUsage)
		return 2
	}
	username := args[1]

	cfg, err := config.FromEnv()
	if err != nil {
		fmt.Fprintln(stderr, "configuration error:", err)
		return 2
	}
	c, _, err := appStoreConfig(cfg)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	ctx := context.Background()
	store, err := appstore.Open(ctx, c)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer store.Close()

	user, err := store.UserByUsername(ctx, username)
	if errors.Is(err, appstore.ErrNotFound) {
		fmt.Fprintf(stderr, "no user %q\n", username)
		return 1
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	password, err := readNewPassword(stdin, stderr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := store.SetPassword(ctx, user.ID, hash); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "Password for %s changed; their sessions were ended.\n", user.Username)
	return 0
}

func readNewPassword(stdin io.Reader, stderr io.Writer) (string, error) {
	if f, ok := stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		fmt.Fprint(stderr, "New password: ")
		a, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(stderr)
		if err != nil {
			return "", err
		}
		fmt.Fprint(stderr, "Repeat it: ")
		b, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(stderr)
		if err != nil {
			return "", err
		}
		if string(a) != string(b) {
			return "", errors.New("the passwords don't match")
		}
		return string(a), nil
	}
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}
