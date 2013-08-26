package copycat

import (
	"crypto/tls"
	"errors"
	"log"

	"code.google.com/p/go-imap/go1/imap"
)

const (
	// TODO: figure out an approp # of conns (GMail allows 15 concurrent IMAP conns)
	MaxImapConns   = 10
	MemcacheServer = "localhost:11211"
)

func NewCopyCat(src InboxInfo, dsts []InboxInfo) (*CopyCat, error) {
	cat := &CopyCat{SourceInfo: src, DestInfos: dsts}
	cat.DestConns = make(map[string][]*imap.Client)
	for i := 0; i < MaxImapConns; i++ {

		sourceConn, err := GetConnection(src, true)
		if err != nil {
			log.Printf("Unable to connect to %s: %s", src.User, err.Error())
			return cat, err
		}
		cat.SourceConns = append(cat.SourceConns, sourceConn)


		for _, dst := range dsts {
			dstConn, err := GetConnection(dst, false)
			if err != nil {
				log.Printf("Unable to connect to %s: %s", src.User, err.Error())
				return cat, err
			}
			if _, exists := cat.DestConns[dst.User]; exists {
				cat.DestConns[dst.User] = append(cat.DestConns[dst.User], dstConn)
			} else {
				cat.DestConns[dst.User] = []*imap.Client{dstConn}
			}

		}
	}

	return cat, nil
}

// CopyCat represents a process waiting to copy
type CopyCat struct {
	// hold on in case of need for reconnect
	SourceInfo InboxInfo
	DestInfos  []InboxInfo

	SourceConns []*imap.Client
	DestConns   map[string][]*imap.Client
}

// Sync will make sure that the dst inbox looks exactly like the src.
func (c *CopyCat) Sync() error {
	return Sync(c.SourceConns, c.DestConns)
}

func (c *CopyCat) SyncAndIdle() (err error) {
	err = Sync(c.SourceConns, c.DestConns)
	if err != nil {
		log.Print("SYNC ERROR: ", err.Error())
		return
	}

	// idle ... sync on any notification...only if we can make sync superfast.
	return
}

// Sync will make sure that the dst inbox looks exactly like the src.
func Sync(src []*imap.Client, dsts map[string][]*imap.Client) (err error) {
	err = SearchAndPurge(src, dsts)
	if err != nil {
		return
	}

	err = SearchAndStore(src, dsts)
	return
}

type Config struct {
	Source InboxInfo
	Dest   []InboxInfo
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

func GetAllMessages(conn *imap.Client) (*imap.Command, error) {
	// get headers and UID for ALL message in src inbox...
	allMsgs, _ := imap.NewSeqSet("")
	allMsgs.Add("1:*")
	cmd, err := imap.Wait(conn.Fetch(allMsgs, "RFC822.HEADER", "UID"))
	if err != nil {
		return &imap.Command{}, err
	}

	return cmd, nil
}

func GetConnection(info InboxInfo, readOnly bool) (*imap.Client, error) {
	conn, err := imap.DialTLS(info.Host, new(tls.Config))
	if err != nil {
		return nil, err
	}

	_, err = conn.Login(info.User, info.Pw)
	if err != nil {
		return nil, err
	}

	_, err = imap.Wait(conn.Select("INBOX", readOnly))
	if err != nil {
		return nil, err
	}

	return conn, nil
}

type WorkRequest struct {
	Value  string
	Header string
	UID    uint32
}
