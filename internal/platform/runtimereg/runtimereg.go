package runtimereg

import "sync"

var vals sync.Map

func Set(key string, v any) { vals.Store(key, v) }

func Get(key string) (any, bool) { return vals.Load(key) }
