package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"loom/internal/auth"
	"loom/internal/store"
	"os"
	"strings"
	"time"
)

func readSecret(stdin bool, value string) (string, error) {
	if stdin {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", err
		}
		return strings.TrimRight(string(data), "\r\n"), nil
	}
	if value == "" {
		return "", errors.New("provide --password-stdin (preferred) or --password")
	}
	return value, nil
}

func runUser(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: loom user create|list|role|disable|enable|passwd")
	}
	sub := args[0]
	fs := flag.NewFlagSet("user "+sub, flag.ContinueOnError)
	data := fs.String("data", "./data", "data directory")
	switch sub {
	case "create":
		email := fs.String("email", "", "email address")
		name := fs.String("name", "", "display name")
		role := fs.String("role", "viewer", "viewer|operator|editor|admin")
		password := fs.String("password", "", "password (prefer --password-stdin)")
		stdin := fs.Bool("password-stdin", false, "read the password from stdin")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		parsed, ok := auth.ParseRole(*role)
		if !ok {
			return errors.New("role must be viewer, operator, editor or admin")
		}
		secret, err := readSecret(*stdin, *password)
		if err != nil {
			return err
		}
		hash, err := auth.HashPassword(secret)
		if err != nil {
			return err
		}
		st, err := openStore(*data)
		if err != nil {
			return err
		}
		defer st.Close()
		user, err := st.CreateUser(store.User{
			Email: *email, Name: *name, Role: string(parsed), PasswordHash: hash,
		})
		if err != nil {
			return err
		}
		fmt.Printf("created %s (%s)\n", user.Email, user.Role)
		return nil
	case "list":
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		st, err := openStore(*data)
		if err != nil {
			return err
		}
		defer st.Close()
		users, err := st.ListUsers()
		if err != nil {
			return err
		}
		for _, user := range users {
			state := "active"
			if user.Disabled {
				state = "disabled"
			}
			fmt.Printf("%s\t%s\t%s\t%s\n", user.ID, user.Email, user.Role, state)
		}
		return nil
	case "role", "disable", "enable":
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() < 1 {
			return errors.New("usage: loom user " + sub + " <email> [role]")
		}
		st, err := openStore(*data)
		if err != nil {
			return err
		}
		defer st.Close()
		user, err := st.GetUserByEmail(fs.Arg(0))
		if err != nil {
			return err
		}
		switch sub {
		case "role":
			if fs.NArg() < 2 {
				return errors.New("usage: loom user role <email> <role>")
			}
			parsed, ok := auth.ParseRole(fs.Arg(1))
			if !ok {
				return errors.New("role must be viewer, operator, editor or admin")
			}
			if user.Role == string(auth.RoleAdmin) && parsed != auth.RoleAdmin {
				if admins, _ := st.CountAdmins(); admins <= 1 {
					return errors.New("cannot demote the last admin")
				}
			}
			if err := st.UpdateUserRole(user.ID, string(parsed)); err != nil {
				return err
			}
			fmt.Printf("%s is now %s\n", user.Email, parsed)
		case "disable", "enable":
			disable := sub == "disable"
			if disable && user.Role == string(auth.RoleAdmin) {
				if admins, _ := st.CountAdmins(); admins <= 1 {
					return errors.New("cannot disable the last admin")
				}
			}
			if err := st.SetUserDisabled(user.ID, disable); err != nil {
				return err
			}
			if disable {
				_ = st.DeleteUserSessions(user.ID, "")
			}
			fmt.Printf("%s disabled=%v\n", user.Email, disable)
		}
		return nil
	case "passwd":
		password := fs.String("password", "", "password (prefer --password-stdin)")
		stdin := fs.Bool("password-stdin", false, "read the password from stdin")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() < 1 {
			return errors.New("usage: loom user passwd --password-stdin <email>")
		}
		secret, err := readSecret(*stdin, *password)
		if err != nil {
			return err
		}
		hash, err := auth.HashPassword(secret)
		if err != nil {
			return err
		}
		st, err := openStore(*data)
		if err != nil {
			return err
		}
		defer st.Close()
		user, err := st.GetUserByEmail(fs.Arg(0))
		if err != nil {
			return err
		}
		if err := st.SetUserPassword(user.ID, hash); err != nil {
			return err
		}
		_ = st.DeleteUserSessions(user.ID, "")
		_ = st.RevokeAPITokensForUser(user.ID)
		fmt.Printf("password updated for %s\n", user.Email)
		return nil
	default:
		return fmt.Errorf("unknown user subcommand %q", sub)
	}
}

func runToken(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: loom token create|list|revoke")
	}
	sub := args[0]
	fs := flag.NewFlagSet("token "+sub, flag.ContinueOnError)
	data := fs.String("data", "./data", "data directory")
	switch sub {
	case "create":
		email := fs.String("email", "", "owner email")
		name := fs.String("name", "api token", "label")
		role := fs.String("role", "", "role (defaults to the owner's role)")
		expires := fs.String("expires", "", "expiry as RFC3339 (optional)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		st, err := openStore(*data)
		if err != nil {
			return err
		}
		defer st.Close()
		user, err := st.GetUserByEmail(*email)
		if err != nil {
			return err
		}
		tokenRole := auth.Role(user.Role)
		if strings.TrimSpace(*role) != "" {
			parsed, ok := auth.ParseRole(*role)
			if !ok {
				return errors.New("role must be viewer, operator, editor or admin")
			}
			if !auth.Role(user.Role).Allows(parsed) {
				return errors.New("token role cannot exceed the owner's role")
			}
			tokenRole = parsed
		}
		raw, err := auth.NewToken()
		if err != nil {
			return err
		}
		var expiresAt *time.Time
		if strings.TrimSpace(*expires) != "" {
			parsed, err := time.Parse(time.RFC3339, *expires)
			if err != nil {
				return errors.New("--expires must be RFC3339")
			}
			expiresAt = &parsed
		}
		created, err := st.CreateAPIToken(store.APIToken{
			UserID: user.ID, Name: *name, Role: string(tokenRole),
			TokenHash: auth.HashToken(raw), ExpiresAt: expiresAt,
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "created token %s for %s (%s); it is shown once\n",
			created.ID, user.Email, tokenRole)
		fmt.Println(raw)
		return nil
	case "list":
		email := fs.String("email", "", "filter by owner email (optional)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		st, err := openStore(*data)
		if err != nil {
			return err
		}
		defer st.Close()
		userID := ""
		if strings.TrimSpace(*email) != "" {
			user, err := st.GetUserByEmail(*email)
			if err != nil {
				return err
			}
			userID = user.ID
		}
		tokens, err := st.ListAPITokens(userID)
		if err != nil {
			return err
		}
		for _, token := range tokens {
			state := "active"
			if token.RevokedAt != nil {
				state = "revoked"
			}
			fmt.Printf("%s\t%s\t%s\t%s\t%s\n",
				token.ID, token.UserID, token.Name, token.Role, state)
		}
		return nil
	case "revoke":
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() < 1 {
			return errors.New("usage: loom token revoke <id>")
		}
		st, err := openStore(*data)
		if err != nil {
			return err
		}
		defer st.Close()
		if err := st.RevokeAPIToken(fs.Arg(0)); err != nil {
			return err
		}
		fmt.Println("revoked")
		return nil
	default:
		return fmt.Errorf("unknown token subcommand %q", sub)
	}
}
