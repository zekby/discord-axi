package keyring

import (
	"github.com/zalando/go-keyring"
	"github.com/zekby/discord-axi/internal/discordo/consts"
)

const keyringService = consts.Name

// Changed from Discordo's single "token" entry: each account profile keeps its
// own secret under its own name, so several accounts can be stored at once.

func GetToken(profile string) (string, error) {
	return keyring.Get(keyringService, profile)
}

func SetToken(profile, token string) error {
	return keyring.Set(keyringService, profile, token)
}

func DeleteToken(profile string) error {
	return keyring.Delete(keyringService, profile)
}
