package handler

import (
	"bytes"
	"encoding/gob"
	"github.com/dgraph-io/badger"
	"io/ioutil"
	"fmt"
)

func deleteConfig(vnfrId string) error {
	return kv.Delete([]byte(vnfrId))
}

func getConfig(vnfrId string, config *VnfrConfig) error {
	kvItem := badger.KVItem{}
	kv.Get([]byte(vnfrId), &kvItem)
	return kvItem.Value(func(bs []byte) error {
		buf := bytes.NewBuffer(bs)
		err := gob.NewDecoder(buf).Decode(config)
		return err
	})
}

func InitDB(persist bool, dir_path string) {
	var dir string
	if !persist {
		dir, _ = ioutil.TempDir(dir_path, "badger")
	} else {
		dir = dir_path
	}
	opt.Dir = dir
	opt.ValueDir = dir
	var err error
	kv, err = badger.NewKV(&opt)
	if err != nil {
		fmt.Errorf("Error while creating database: %v", err)
	}
}

func SaveConfig(vnfrId string, config VnfrConfig) error {
	//lock.Lock()
	//defer lock.Unlock()
	buf := new(bytes.Buffer)
	err := gob.NewEncoder(buf).Encode(config)
	if err != nil {
		return err
	}
	return kv.Set([]byte(vnfrId), buf.Bytes(), 0x00)
}