package auth

import (
	"crypto/rand"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/golang-jwt/jwt/v5"
	_ "github.com/golang-jwt/jwt/v5"
	"math/big"
	"os"
	"time"
)

// TokenType distinguishes what a token is allowed to do. Without it, a token
// minted for a $12 sensor is indistinguishable from an operator's token, and
// any device could drive the admin endpoints.
type TokenType string

const (
	TokenTypeDevice TokenType = "device"
	TokenTypeAdmin  TokenType = "admin"
)

// DeviceClaims is the payload carried inside a device token.
//
// The client key is deliberately absent. A JWT is signed, not encrypted, so
// anything placed in here is readable by whoever holds the token.
type DeviceClaims struct {
	Type            TokenType `json:"typ"`
	MacAddress      string    `json:"mac_address"`
	FirmwareVersion string    `json:"firmware_version"`
}

// jwtSecret returns the signing secret, refusing to operate without one.
//
// os.Getenv returns "" for an unset variable, and HMAC will happily sign and
// verify with an empty key -- which makes every token forgeable by anyone who
// knows the payload shape. Failing closed is the only safe behaviour.
func jwtSecret() ([]byte, error) {
	secret := os.Getenv("JWT_SECRET")
	if len(secret) < 32 {
		return nil, errors.New("JWT_SECRET is unset or shorter than 32 bytes")
	}
	return []byte(secret), nil
}

// ParseToken verifies a token's signature and expiry and returns its claims.
//
// WithValidMethods pins the algorithm to HS256. Without it the parser accepts
// whatever the token's own header asks for, which is the entry point for the
// classic algorithm-confusion family of attacks.
func ParseToken(token string) (jwt.MapClaims, error) {
	secret, secretErr := jwtSecret()
	if secretErr != nil {
		return nil, secretErr
	}
	verifyToken, err := jwt.Parse(token, func(token *jwt.Token) (interface{}, error) {
		return secret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil {
		return nil, err
	}
	if !verifyToken.Valid {
		return nil, errors.New("invalid token")
	}
	claims, ok := verifyToken.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.New("unexpected claims format")
	}
	return claims, nil
}

// ValidateToken reports whether a token is well formed, correctly signed and
// unexpired. Retained for callers that only need a yes/no answer.
func ValidateToken(token string) error {
	_, err := ParseToken(token)
	return err
}

// DeviceClaimsFromToken extracts the device claims nested under "data".
func DeviceClaimsFromToken(claims jwt.MapClaims) (DeviceClaims, error) {
	var out DeviceClaims
	data, ok := claims["data"].(map[string]interface{})
	if !ok {
		return out, errors.New("token carries no claim data")
	}
	if typ, ok := data["typ"].(string); ok {
		out.Type = TokenType(typ)
	}
	if mac, ok := data["mac_address"].(string); ok {
		out.MacAddress = mac
	}
	if fw, ok := data["firmware_version"].(string); ok {
		out.FirmwareVersion = fw
	}
	if out.MacAddress == "" {
		return out, errors.New("token carries no mac address")
	}
	return out, nil
}

func GenToken(data interface{}, expiresIn time.Duration) (string, error) {
	secret, secretErr := jwtSecret()
	if secretErr != nil {
		return "", secretErr
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256,
		jwt.MapClaims{
			"data": data,
			"exp":  time.Now().Add(expiresIn).Unix(),
		})
	return token.SignedString(secret)
}

func GenSalt() ([]byte, error) {
	salt := make([]byte, 25)
	_, randErr := rand.Read(salt[:])
	if randErr != nil {
		return nil, randErr
	}
	return salt, nil
}

const letterBytes = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ123456789!@#$%^&*()_"

func randPassword(size int) (string, error) {
	b := make([]byte, size)
	index, randErr := rand.Int(rand.Reader, big.NewInt(int64(len(letterBytes))))
	if randErr != nil {
		return "", randErr
	}
	for i := range b {
		b[i] = letterBytes[index.Int64()]
		index, randErr = rand.Int(rand.Reader, big.NewInt(int64(len(letterBytes))))
		if randErr != nil {
			return "", randErr
		}
	}
	return string(b), randErr
}

func GenKey() (string, []byte, error) {
	salt, saltError := GenSalt()
	if saltError != nil {
		return "", nil, saltError
	}
	key, keyErr := randPassword(25)
	if keyErr != nil {
		return "", nil, keyErr
	}
	return key, salt, nil
}

func EncryptString(str string, salt []byte) (string, error) {
	stringBytes := []byte(str)
	hasher := sha512.New()
	saltedString := append(stringBytes, salt...)
	hasher.Write(saltedString)
	hashedStringBytes := hasher.Sum(nil)
	HashedSaltedString := hex.EncodeToString(hashedStringBytes)
	return HashedSaltedString, nil
}

// SecureCompareKey compares a submitted key against a stored one in constant
// time. A plain != leaks the length of the matching prefix through timing,
// which is enough to recover a key one character at a time.
func SecureCompareKey(storedKey string, submittedKey string, salt []byte) (bool, error) {
	expected, expectedErr := EncryptString(storedKey, salt)
	if expectedErr != nil {
		return false, fmt.Errorf("hashing stored key: %w", expectedErr)
	}
	provided, providedErr := EncryptString(submittedKey, salt)
	if providedErr != nil {
		return false, fmt.Errorf("hashing submitted key: %w", providedErr)
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) == 1, nil
}
