package copycat

import (
	// "crypto/tls"
	"github.com/sbinet/go-imap/go1/imap"

	"sync"
)

type InboxInfo struct {
	User string
	Pw   string
	Host string
}

func NewCopyCat(srcInfo InboxInfo, dstInfo InboxInfo, catNum int) (cat *CopyCat, err error) {
	
	// connect to imap!
	
	return
}

func NewInboxInfo(id string, pw string, host string) (info InboxInfo, err error) {
	return 
}

type Config struct {
	Source InboxInfo
	Dest   []InboxInfo
}

// CopyCat represents a process waiting to copy
type CopyCat struct {
	// hold on in case of need for reconnect.
	SourceInfo InboxInfo
	DestInfo   InboxInfo

	dest *imap.Client
	src  *imap.Client
}

// Sync will make sure that the dst inbox looks exactly like the src.
func (c *CopyCat) Sync(wg *sync.WaitGroup) error {
	defer wg.Done()
	return Sync(c.src, c.dest)
}

// Sync will make sure that the dst inbox looks exactly like the src.
func Sync(src *imap.Client, dst *imap.Client) (err error) {
	err = syncAdd(src, dst)
	if err != nil {
		return
	}

	err = syncPurge(src, dst)
	return
}

// syncAdd will add any files in src that do not exist in dst.
func syncAdd(src *imap.Client, dst *imap.Client) error {
	// for each message UID src...

	// do a UIDSearch on dst

	// if it doesnt exist, do a src.UIDFetch -> dst.UIDStore

	return nil
}

// syncPurge will remove any messages in dst that do not exist in src.
func syncPurge(src *imap.Client, dst *imap.Client) error {
	// for each message UID dst...

	// do a UIDSearch on src

	// if it doesnt exist, do a dst.UIDStore with \Deleted flag

	// expunge at the end

	return nil
}

func (c *CopyCat) SyncAndIdle(wg *sync.WaitGroup) (err error) {
	defer wg.Done()
	
	err = Sync(c.src, c.dest)
	if err != nil {
		return
	}

	// idle ... sync on any notification...only if we can make sync superfast.
	return
}
