package copycat

import (
	"crypto/tls"
	"errors"
	"time"
	"log"

	"code.google.com/p/go-imap/go1/imap"
)

const (
	// TODO: figure out an approp # of conns (GMail allows 15 concurrent IMAP conns)
	MemcacheServer = "localhost:11211"
)

func NewCopyCat(src InboxInfo, dsts []InboxInfo, connsPerInbox int) (*CopyCat, error) {
	// pull user names for logging
	var dstUsers []string
	for _, usr := range dsts {
		dstUsers = append(dstUsers, usr.User)
	}
	log.Printf("Creating CopyCat to to sync %s's contents to the following mailbox(s):  %s", src.User, dstUsers)

	cat := &CopyCat{SourceInfo: src, DestInfos: dsts}
	//initiate connections
	cat.DestConns = make(map[string][]*imap.Client)
	for i := 0; i < connsPerInbox; i++ {
		// initiate source connections
		sourceConn, err := GetConnection(src, true)
		if err != nil {
			log.Printf("Unable to connect to %s: %s", src.User, err.Error())
			return cat, err
		}
		cat.SourceConns = append(cat.SourceConns, sourceConn)

		// initiate destination connections
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
	log.Printf("CopyCat created with %d connections per inbox", connsPerInbox)
	return cat, nil
}

// CopyCat represents a process waiting to copy
type CopyCat struct {
	SourceInfo InboxInfo
	DestInfos  []InboxInfo

	SourceConns []*imap.Client
	DestConns   map[string][]*imap.Client
}

// Sync will make sure that the dst inbox looks exactly like the src.
func (c *CopyCat) Sync() error {
	return Sync(c.SourceConns, c.DestConns)
}

// Idle will create a single connection to each mailbox (src & dests) and use them
// to idle and update mailboxes.
func (c *CopyCat) Idle() error {
	log.Printf("connecting to idle source: %s", c.SourceInfo.User)
	srcConn, err := GetConnection(c.SourceInfo, true)
	if err != nil {
		log.Printf("Problems connecting to source (%s) for Idle", c.SourceInfo.User)
		return err
	}
	defer srcConn.Close(false)

	var dstConns []*imap.Client
	for _, dst := range c.DestInfos {
		log.Printf("connecting to idle destination: %s", dst.User)
		dstConn, err := GetConnection(dst, false)
		if err != nil {
			log.Printf("Problems connecting to destination (%s) or Idle", dst.User)
			return err
		}
		defer dstConn.Close(false)
		dstConns = append(dstConns, dstConn)
	}

	return Idle(srcConn, dstConns)
}

func (c *CopyCat) SyncAndIdle() (err error) {

	// kick off sync as a goroutine if we plan on idling.
	// Messages could come in/be deleted after sync makes its initial
	// query against the source database. We want Idle to
	// pick up those changes.
	go func() {
		err = Sync(c.SourceConns, c.DestConns)
		if err != nil {
			log.Print("SYNC ERROR: ", err.Error())
		}
	}()

	// idle ... sync on any notification...only if we can make sync superfast.
	err = c.Idle()
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

type WorkRequest struct {
	Value  string
	Header string
	UID    uint32
}
