package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"

	"copycat-imap/copycat"

	"github.com/jprobinson/go-utils/utils"
)

var (
	// cli accepts a host id/pw/host
	srcId   = flag.String("src-id", "", "The login ID for the source mailbox.")
	srcPw   = flag.String("src-pw", "", "The login password for the source mailbox.")
	srcHost = flag.String("src-host", "", "The imap host for the source mailbox.")

	// and single dest id/pw/host
	dstId   = flag.String("dst-id", "", "The login ID for the destincation mailbox.")
	dstPw   = flag.String("dst-pw", "", "The login password for the destincation mailbox.")
	dstHost = flag.String("dst-host", "", "The imap host for the destincation mailbox.")

	// or multiple dest inbox by config file
	configFile    = flag.String("config-file", "", "Location of a config file to pass in source and destination login information. Use -example-config to see the format.")
	exampleConfig = flag.Bool("example-config", false, "View an example layout for a json config file meant to hold multiple destination accounts.")

	// single run or idle and wait
	idle       = flag.Bool("idle", false, "Sync the mailboxes and then idle and wait for updates. Creates an additional connection for each inbox.")
	sync       = flag.Bool("sync", true, "Run a sync of the mailboxes. Flag helpful for skipping sync with bandwidth usage is limited.")
	purge      = flag.Bool("purge", false, "During the sync this will purge any destination messages that do not exist in the source.")
	quicksync  = flag.Bool("quick", false, "Starts a quick sync that will only look to 'sync' the last 'quick-count' messages.")
	quickcount = flag.Int("quick-count", 500, "The number of messages to look for with a quick scan.")

	// # of IMAP connections per mailbox
	conns = flag.Int("c", 2, "The number of concurrent IMAP connections for each inbox during Syncing. Large #s may run faster but you may risk reaching connection/bandwidth limits for you email provider.")

	// accept log file too
	logFile = flag.String("log", "", "Location to write logs to. stderr by default. If set, a HUP signal will handle logrotate.")
	dbFile  = flag.String("db", "/var/copycat/messages", "path for message storage")
)

func main() {

	flag.Parse()

	if *exampleConfig {
		fmt.Print(getExampleConfig())
		return
	}

	if *conns <= 0 {
		*conns = 10
	}

	var srcInfo copycat.InboxInfo
	var dstInfos []copycat.InboxInfo

	if len(*configFile) == 0 {
		// put together info from input
		var err error
		srcInfo, err = copycat.NewInboxInfo(*srcId, *srcPw, *srcHost)
		errCheck(err, "Source Info")

		var dstInfo copycat.InboxInfo
		dstInfo, err = copycat.NewInboxInfo(*dstId, *dstPw, *dstHost)
		errCheck(err, "Destination Info")
		dstInfos = append(dstInfos, dstInfo)

	} else {
		//READ THE CONFIG FILE
		cFile, err := os.Open(*configFile)
		errCheck(err, "Config File")

		configBytes, err := ioutil.ReadAll(cFile)
		errCheck(err, "Config File")
		cFile.Close()

		var config copycat.Config
		err = json.Unmarshal(configBytes, &config)
		errCheck(err, "Config File")

		srcInfo = config.Source
		err = srcInfo.Validate()
		errCheck(err, "Source Creds")

		dstInfos = config.Dest
		for _, info := range dstInfos {
			err = info.Validate()
			errCheck(err, "Destination Creds")
		}
	}

	// check log flag, setup logger if set.
	if len(*logFile) > 0 {
		logger := utils.DefaultLogSetup{LogFile: *logFile}
		logger.SetupLogging()
		go utils.ListenForLogSignal(logger)
	}

start:
	cat, err := copycat.NewCopyCat(srcInfo, dstInfos, *conns, *sync, *idle)
	if err != nil {
		log.Printf("Problems creating new copycat: %s", err.Error())
	}

	if !*quicksync {
		*quickcount = 0
	}

	switch {
	case *idle:
		cat.Idle(*sync, *purge, *dbFile)
		// if idle ended, something's up. just restart.
		log.Printf("Idle unexpectedly quit. attempting to close conns...")
		cat.Close()
		log.Print("Conns closed. restarting process.")
		goto start
	case *sync:
		cat.Sync(*purge, *dbFile, *quickcount)
		cat.Close()
	}
}

func errCheck(err error, msg string) {
	if err != nil {
		log.Printf("Invalid %s: %s", msg, err.Error())
		os.Exit(1)
	}
}

func getExampleConfig() string {
	return `
	
	{
	    "source": {
	        "user": "source_user_name",
	        "pw": "source_pa$$w0rd",
	        "host": "imap.source.com"
	    },
	    "dest": [
	        {
	            "user": "dest1_user_name",
	            "pw": "dest1_pa$$w0rd",
	            "host": "imap.dest1.com"
	        },
	        {
	            "user": "dest2_user_name",
	            "pw": "dest2_pa$$w0rd",
	            "host": "imap.dest2.com"
	        }
	    ]
	}
	
`
}
