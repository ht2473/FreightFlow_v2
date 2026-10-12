package api

import (
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"
)

// PermAuthenticated — операция доступна любому вошедшему пользователю.
const PermAuthenticated = "authenticated"

// Operation — операция спецификации с требуемым правом.
type Operation struct {
	ID         string
	Method     string
	Path       string
	Permission string
}

type specDoc struct {
	Paths map[string]map[string]struct {
		OperationID string `yaml:"operationId"`
		Permission  string `yaml:"x-permission"`
	} `yaml:"paths"`
}

// Operations разбирает спецификацию и возвращает операции с правами из x-permission.
func Operations(spec []byte) ([]Operation, error) {
	var doc specDoc
	if err := yaml.Unmarshal(spec, &doc); err != nil {
		return nil, fmt.Errorf("parse openapi: %w", err)
	}
	var ops []Operation
	for path, methods := range doc.Paths {
		for method, op := range methods {
			if op.OperationID == "" {
				continue
			}
			ops = append(ops, Operation{ID: op.OperationID, Method: strings.ToUpper(method), Path: path, Permission: op.Permission})
		}
	}
	return ops, nil
}

// permissionIndex сопоставляет имя операции ogen (operationId с заглавной буквы) с правом.
func permissionIndex(ops []Operation) map[string]string {
	idx := make(map[string]string, len(ops))
	for _, op := range ops {
		idx[strings.ToLower(op.ID)] = op.Permission
	}
	return idx
}
