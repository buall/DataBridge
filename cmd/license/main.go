package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"databridge/internal/license"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "keygen":
		err = keygen(os.Args[2:])
	case "sign":
		err = sign(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: databridge-license keygen | databridge-license sign --request FILE --days DAYS")
	os.Exit(2)
}

func keygen(arguments []string) error {
	flags := flag.NewFlagSet("keygen", flag.ContinueOnError)
	privatePath := flags.String("private", ".local-license/signing-private.key", "private key output path")
	publicPath := flags.String("public", ".local-license/signing-public.key", "public key output path")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if _, err := os.Stat(*privatePath); err == nil {
		return fmt.Errorf("private key already exists: %s", *privatePath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	publicKey, privateKey, err := license.GenerateSigningKey()
	if err != nil {
		return err
	}
	if err := writeKey(*privatePath, privateKey, 0o600); err != nil {
		return err
	}
	if err := writeKey(*publicPath, publicKey, 0o644); err != nil {
		return err
	}
	fmt.Println("public_key=" + license.EncodeKey(publicKey))
	return nil
}

func sign(arguments []string) error {
	flags := flag.NewFlagSet("sign", flag.ContinueOnError)
	privatePath := flags.String("private", ".local-license/signing-private.key", "signing private key path")
	requestPath := flags.String("request", "", "activation request path")
	outputPath := flags.String("output", "", "optional license output path")
	validDays := flags.Int("days", 0, "license validity in days")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *requestPath == "" {
		return errors.New("request file is required")
	}
	if *validDays < 1 || *validDays > 36500 {
		return errors.New("days must be between 1 and 36500")
	}
	privateData, err := os.ReadFile(*privatePath)
	if err != nil {
		return err
	}
	privateKey, err := license.ParsePrivateKey(string(privateData))
	if err != nil {
		return err
	}
	requestData, err := os.ReadFile(*requestPath)
	if err != nil {
		return err
	}
	request, err := license.ParseRequest(requestData)
	if err != nil {
		return err
	}
	deploymentPublicKey, err := license.ParsePublicKey(request.DeploymentPublicKey)
	if err != nil {
		return err
	}
	licenseID, err := newLicenseID()
	if err != nil {
		return err
	}
	issuedAt := time.Now().UTC()
	expiresAt := issuedAt.AddDate(0, 0, *validDays)
	claims := license.Claims{
		SchemaVersion:     license.SchemaVersion,
		LicenseID:         licenseID,
		CustomerID:        request.DeploymentID,
		DeploymentID:      request.DeploymentID,
		DeploymentKeyHash: license.PublicKeyHash(deploymentPublicKey),
		Product:           license.ProductName,
		Edition:           "full",
		Features:          []string{"migration", "backup", "restore"},
		IssuedAt:          issuedAt,
		ExpiresAt:         &expiresAt,
	}
	data, err := license.Sign(privateKey, claims)
	if err != nil {
		return err
	}
	destination := *outputPath
	if destination == "" {
		destination = filepath.Join(filepath.Dir(*requestPath), "databridge-license-"+request.DeploymentID+".json")
	}
	if directory := filepath.Dir(destination); directory != "." {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return err
		}
	}
	if err := os.WriteFile(destination, append(data, '\n'), 0o600); err != nil {
		return err
	}
	fmt.Printf("license written to %s\n", destination)
	fmt.Printf("valid for %d days, until %s\n", *validDays, expiresAt.Format(time.RFC3339))
	return nil
}

func writeKey(path string, key []byte, mode os.FileMode) error {
	if directory := filepath.Dir(path); directory != "." {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(license.EncodeKey(key)+"\n"), mode)
}

func newLicenseID() (string, error) {
	var value [12]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate license ID: %w", err)
	}
	return "lic_" + hex.EncodeToString(value[:]), nil
}
