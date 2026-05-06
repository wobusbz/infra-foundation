package session

import "math/rand/v2"

const base62Chars = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

type SessionID string

func (t SessionID) String() string { return string(t) }

func GenerateSessionID() SessionID {
	var buf [16]byte
	for i := range buf {
		buf[i] = base62Chars[rand.IntN(62)]
	}
	return SessionID(string(buf[:]))
}
