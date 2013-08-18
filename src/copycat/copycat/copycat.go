package copycat

import (
	"crypto/tls"
	"sync"
	"errors"
	
	"github.com/sbinet/go-imap/go1/imap"
)

// CopyCat represents a process waiting to copy
type CopyCat struct {
	// hold on in case of need for reconnect
	SourceInfo InboxInfo
	DestInfo   InboxInfo

	dest *imap.Client
	src  *imap.Client
	
	// for logging purposes
	num int
}

// NewCopyCat will create a new CopyCat instance initialize the IMAP connections.
func NewCopyCat(srcInfo InboxInfo, dstInfo InboxInfo, catNum int) (cat *CopyCat, err error) {
	cat = &CopyCat{SourceInfo: srcInfo, DestInfo: dstInfo, num: catNum}
	
	cat.src, err = getConnection(cat.SourceInfo)
	if err != nil {
		return
	}
	
	cat.dest, err = getConnection(cat.DestInfo)
	if err != nil {
		return
	}

	return
}

// Sync will make sure that the dst inbox looks exactly like the src.
func (c *CopyCat) Sync(wg *sync.WaitGroup) error {
	defer wg.Done()
	return Sync(c.src, c.dest)
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

// Sync will make sure that the dst inbox looks exactly like the src.
func Sync(src *imap.Client, dst *imap.Client) (err error) {
	err = syncAdd(src, dst)
	if err != nil {
		return
	}

	err = syncPurge(src, dst)
	return
}

type InboxInfo struct {
	User string
	Pw   string
	Host string
}

func NewInboxInfo(id string, pw string, host string) (info InboxInfo, err error) {
	info = InboxInfo{User: id, Pw: pw, Host: host}
	return info, info.Validate()
}

func (i *InboxInfo) Validate() error {
	if len(i.User) == 0 {
		return errors.New("Login ID is required.")
	}

	if len(i.Pw) == 0 {
		return errors.New("Login Password is required.")
	}

	if len(i.Host) == 0 {
		return errors.New("IMAP Host is required.")
	}

	return nil
}

type Config struct {
	Source InboxInfo
	Dest   []InboxInfo
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

func getConnection(info InboxInfo) (*imap.Client, error) {
	conn, err := imap.DialTLS(info.Host, new(tls.Config))
	if err != nil {
		return nil, err
	}

	_, err = conn.Login(info.User, info.Pw)
	if err != nil {
		return nil, err
	}


	_, err = imap.Wait(conn.Select("INBOX", false))
	if err != nil {
		return nil, err
	}

	return conn, nil
}
