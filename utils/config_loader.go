package utils

import (
	"github.com/BurntSushi/toml"
)

func LoadTOMLConfig(filename string, cfg interface{}) error {
	_, err := toml.DecodeFile(filename, cfg)
	return err
}
