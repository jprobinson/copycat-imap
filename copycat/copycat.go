package copycat

import (
	"crypto/tls"
	"errors"
	"log"
	"time"

	"code.google.com/p/go-imap/go1/imap"
)

const (
	MemcacheServer = "localhost:11211"
)

func NewCopyCat(src InboxInfo, dsts []InboxInfo, connsPerInbox int) (cat *CopyCat, err error) {
	// pull user names for logging
	var dstUsers []string
	for _, usr := range dsts {
		dstUsers = append(dstUsers, usr.User)
	}
	log.Printf("Creating CopyCat to to sync %s's contents to the following mailbox(s):  %s", src.User, dstUsers)

	cat = &CopyCat{SourceInfo: src, DestInfos: dsts}

	if cat.SyncConns, err = initiateConnections(src, dsts, connsPerInbox); err != nil {
		log.Printf("unable to initiate sync connections: %s", err.Error())
		return cat, err
	}
	log.Printf("created %d connections per inbox for syncing", connsPerInbox)

	if cat.IdleConns, err = initiateConnections(src, dsts, 1); err != nil {
		log.Printf("unable to initiate sync connections: %s", err.Error())
		return cat, err
	}
	log.Printf("created 1 connection per inbox for idling/updating", connsPerInbox)

	return cat, nil
}

// CopyCat represents a process waiting to copy
type CopyCat struct {
	SourceInfo InboxInfo
	DestInfos  []InboxInfo

	SyncConns conns
	IdleConns conns
}

type conns struct {
	Source []*imap.Client
	Dest   map[string][]*imap.Client
}

// Sync will make sure that the dst inbox looks exactly like the src.
func (c *CopyCat) Sync() error {
	return Sync(c.SyncConns.Source, c.SyncConns.Dest)
}

// Idle will optionally sync the mailboxes, wait for updates
// from the imap server and update the destinations appropriately.
func (c *CopyCat) Idle(runSync bool) (err error) {
	// setting up a channel for Idle to request purges.
	// purges will be helpful when we receive a 'expunge' command
	// or an unexpected 'EXISTS' command to get our inboxes back in sync.
	// Channel has a buffer because we dont want idle to be blocked while making
	// a request
	purgeRequests := make(chan bool, 100)

	// kick off sync as a goroutine if we plan on idling.
	// Messages could come in/be deleted after sync makes its initial
	// query against the source database. We want Idle to
	// pick up those changes.
	go func() {
		if runSync {
			err = Sync(c.SyncConns.Source, c.SyncConns.Dest)
			if err != nil {
				log.Print("SYNC ERROR: ", err.Error())
			}
		}

		for _ = range purgeRequests {
			err = SearchAndPurge(c.SyncConns.Source, c.SyncConns.Dest)
			if err != nil {
				log.Print("There was an error during the purge: (%s)", err.Error())
			}
		}
	}()

	// setup the conns for Idling
	srcConn := c.IdleConns.Source[0]
	var dstConns []*imap.Client
	for _, conns := range c.IdleConns.Dest {
		dstConns = append(dstConns, conns[0])
	}

	// idle ... sync on any notification...only if we can make sync superfast.
	err = Idle(srcConn, dstConns, purgeRequests)
	if err != nil {
		log.Print("IDLE ERROR: ", err.Error())
	}

	return
}

// Sync will make sure that the dst inbox looks exactly like the src.
func Sync(src []*imap.Client, dsts map[string][]*imap.Client) (err error) {
	log.Print("beginning sync...")
	err = SearchAndPurge(src, dsts)
	if err != nil {
		log.Print("There was an error during the purge. (%s) quitting process.", err.Error())
		return
	}

	err = SearchAndStore(src, dsts)
	if err != nil {
		log.Print("There was an error during the store. (%s) quitting process.", err.Error())
	}
	log.Print("sync complete")
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

type MessageData struct {
	InternalDate time.Time
	Body         []byte
}

func FetchMessage(conn *imap.Client, messageUID uint32) (msg MessageData, err error) {
	seq, _ := imap.NewSeqSet("")
	seq.AddNum(messageUID)
	var cmd *imap.Command
	cmd, err = imap.Wait(conn.UIDFetch(seq, "INTERNALDATE", "BODY[]"))
	if err != nil {
		log.Printf("Unable to fetch message (%d): %s", messageUID, err.Error())
		return
	}

	if len(cmd.Data) == 0 {
		log.Printf("Unable to fetch message (%d) from src: NO DATA", messageUID)
		return msg, errors.New("message not found")
	}

	msgFields := cmd.Data[0].MessageInfo().Attrs
	msg = MessageData{InternalDate: imap.AsDateTime(msgFields["INTERNALDATE"]), Body: imap.AsBytes(msgFields["BODY[]"])}
	return msg, nil
}

func AppendMessage(conn *imap.Client, messageData MessageData) error {
	_, err := imap.Wait(conn.Append("INBOX", imap.NewFlagSet("UnSeen"), &messageData.InternalDate, imap.NewLiteral(messageData.Body)))
	return err
}

func AddDeletedFlag(conn *imap.Client, uid uint32) error {
	seqSet, _ := imap.NewSeqSet("")
	seqSet.AddNum(uid)
	_, err := conn.UIDStore(seqSet, "+FLAGS", imap.NewFlagSet(`\Deleted`))
	return err
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

func initiateConnections(srcInfo InboxInfo, dstInfos []InboxInfo, connsPerInbox int) (conns conns, err error) {
	//initiate connections
	var srcConns []*imap.Client
	dstConns := make(map[string][]*imap.Client)
	for i := 0; i < connsPerInbox; i++ {
		// initiate source connections
		var sourceConn *imap.Client
		sourceConn, err = GetConnection(srcInfo, true)
		if err != nil {
			log.Printf("Unable to connect to %s: %s", srcInfo.User, err.Error())
			return
		}
		srcConns = append(srcConns, sourceConn)

		// initiate destination connections
		for _, dst := range dstInfos {
			var dstConn *imap.Client
			if dstConn, err = GetConnection(dst, false); err != nil {
				log.Printf("Unable to connect to %s: %s", dst.User, err.Error())
				return
			}

			if _, exists := dstConns[dst.User]; exists {
				dstConns[dst.User] = append(dstConns[dst.User], dstConn)
			} else {
				dstConns[dst.User] = []*imap.Client{dstConn}
			}

		}
	}

	conns.Source = srcConns
	conns.Dest = dstConns
	return conns, nil
}

type WorkRequest struct {
	Value  string
	Header string
	UID    uint32
}
