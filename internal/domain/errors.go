package domain

import "errors"

var (
	ErrAlreadyCertified = errors.New("content already certified")
	ErrNotFound         = errors.New("certificate not found")
)
