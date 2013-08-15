package main

import (
	"time"

	"crypto/tls"
	"github.com/sbinet/go-imap/go1/imap"
)

// CopyCat represents a process waiting to copy
type CopyCat struct {
	
	// hold on in case of need for reconnect.
	user     string
	password string
	host     string


	dest *imap.Client
	src  *imap.Client
}

// Sync will make sure all messages in the dest inbox
// exist in the source inbox
func (c *CopyCat) Sync() error {
	// check message counts
	
	// if they're off, find out some clever way to compare the 2 quickly.
	
	// as you compare, 
}

func (c *CopyCat) ListenAndSync() err {
	
	// sync
	
	// idle ... sync on any notification...only if we can make sync superfast.
}

func main() {

	// cli accepts a host id/pw/host and single dest id/pw/host

	// or multiple dest inbox by config file

	// single run or idle and wait

	// accept log file too
	
	// for each dest, spawn a CopyCat
}
