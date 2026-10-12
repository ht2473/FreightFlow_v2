package app

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"freightflow/internal/identity"
)

func runAdmin(ctx context.Context, env *Env, args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(env.Stderr, "Использование: freightflow admin create-user | reset-2fa")
		return ErrUsage
	}
	switch args[0] {
	case "create-user":
		return adminCreateUser(ctx, env, args[1:])
	case "reset-2fa":
		return adminReset2FA(ctx, env, args[1:])
	default:
		fmt.Fprintf(env.Stderr, "неизвестная команда admin %q\n", args[0])
		return ErrUsage
	}
}

// adminCreateUser создаёт учётную запись из командной строки. Пароль читается из переменной
// FF_ADMIN_PASSWORD или из стандартного ввода, чтобы не оставлять его в истории команд.
func adminCreateUser(ctx context.Context, env *Env, args []string) error {
	fs := flag.NewFlagSet("admin create-user", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	email := fs.String("email", "", "адрес электронной почты")
	name := fs.String("name", "", "имя")
	roles := fs.String("roles", identity.RoleAdmin, "роли через запятую")
	if err := fs.Parse(args); err != nil {
		return ErrUsage
	}
	if *email == "" || *name == "" {
		fmt.Fprintln(env.Stderr, "Использование: freightflow admin create-user -email ... -name ... [-roles admin]")
		return ErrUsage
	}
	password := os.Getenv("FF_ADMIN_PASSWORD")
	if password == "" {
		fmt.Fprint(env.Stderr, "Пароль (не менее 12 символов): ")
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return fmt.Errorf("read password: %w", err)
		}
		password = strings.TrimRight(line, "\r\n")
	}

	pool, err := openDatabase(ctx, env)
	if err != nil {
		return err
	}
	defer pool.Close()
	ids, err := newIdentity(ctx, env, pool)
	if err != nil {
		return err
	}
	u, err := ids.CreateUser(ctx, nil, identity.CreateUserInput{
		Email: *email, Name: *name, Password: password, Roles: splitList(*roles),
	}, identity.RequestMeta{UserAgent: "freightflow admin"})
	var ve *identity.ValidationError
	if errors.As(err, &ve) {
		return fmt.Errorf("%s", ve.Message)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(env.Stdout, "Создан пользователь %s (%s), роли: %s\n", u.Email, u.ID, strings.Join(u.Roles, ", "))
	return nil
}

func adminReset2FA(ctx context.Context, env *Env, args []string) error {
	fs := flag.NewFlagSet("admin reset-2fa", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	email := fs.String("email", "", "адрес электронной почты")
	if err := fs.Parse(args); err != nil || *email == "" {
		fmt.Fprintln(env.Stderr, "Использование: freightflow admin reset-2fa -email ...")
		return ErrUsage
	}
	pool, err := openDatabase(ctx, env)
	if err != nil {
		return err
	}
	defer pool.Close()
	ids, err := newIdentity(ctx, env, pool)
	if err != nil {
		return err
	}
	users, err := ids.ListUsers(ctx)
	if err != nil {
		return err
	}
	target := strings.ToLower(strings.TrimSpace(*email))
	for _, u := range users {
		if u.Email == target {
			if err := ids.DisableTOTP(ctx, nil, u.ID, identity.RequestMeta{UserAgent: "freightflow admin"}); err != nil {
				return err
			}
			fmt.Fprintf(env.Stdout, "Двухфакторная защита пользователя %s сброшена\n", u.Email)
			return nil
		}
	}
	return fmt.Errorf("user %s not found", target)
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}
