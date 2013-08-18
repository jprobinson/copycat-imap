package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"sync"

	"copycat/copycat"

	"github.com/jprobinson/go-utils/utils"
)

func main() {

	// cli accepts a host id/pw/host
	var srcId string
	flag.StringVar(&srcId, "src-id", "", "The login ID for the source mailbox.")

	var srcPw string
	flag.StringVar(&srcPw, "src-pw", "", "The login password for the source mailbox.")

	var srcHost string
	flag.StringVar(&srcHost, "src-host", "", "The imap host for the source mailbox.")

	// and single dest id/pw/host
	var dstId string
	flag.StringVar(&dstId, "dst-id", "", "The login ID for the destincation mailbox.")

	var dstPw string
	flag.StringVar(&dstPw, "dst-pw", "", "The login password for the destincation mailbox.")

	var dstHost string
	flag.StringVar(&dstHost, "dst-host", "", "The imap host for the destincation mailbox.")

	// or multiple dest inbox by config file
	var configFile string
	flag.StringVar(&configFile, "config-file", "", "Location of a config file to pass in source and destination login information. Use -example-config to see the format.")

	var exampleConfig bool
	flag.BoolVar(&exampleConfig, "example-config", false, "View an example layout for a json config file meant to hold multiple destination accounts.")

	// single run or idle and wait
	var idle bool
	flag.BoolVar(&idle, "idle", false, "Sync the mailboxes and then idle and wait for updates.")

	// accept log file too
	var logFile string
	flag.StringVar(&logFile, "log", "stderr", "Location to write logs to. stderr by default. If set, a HUP signal will handle logrotate.")

	flag.Parse()

	if exampleConfig {
		fmt.Print(getExampleConfig())
		return
	}

	var srcInfo copycat.InboxInfo
	var dstInfos []copycat.InboxInfo

	if len(configFile) == 0 {
		// put together info from input
		var err error
		srcInfo, err = copycat.NewInboxInfo(srcId, srcPw, srcHost)
		errCheck(err, "Source Info")

		var dstInfo copycat.InboxInfo
		dstInfo, err = copycat.NewInboxInfo(dstId, dstPw, dstHost)
		errCheck(err, "Destination Info")
		dstInfos = append(dstInfos, dstInfo)

	} else {
		//READ THE CONFIG FILE

		cFile, err := os.Open(configFile)
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

	// CHECK LOG FLAG & SETUP - create log utils in github...sorry NYT, they mine!
	if len(logFile) > 0 {
		logger := utils.DefaultLogSetup{LogFile: logFile}
		logger.SetupLogging()
		go utils.ListenForLogSignal(logger)
	}

	// start the work
	var cats sync.WaitGroup
	for catNum, dstInfo := range dstInfos {
		cat, err := copycat.NewCopyCat(srcInfo, dstInfo, catNum)
		errCheck(err, "IMAP Connection")
		cats.Add(1)

		if idle {
			go cat.SyncAndIdle(&cats)

		} else {

			go cat.Sync(&cats)
		}
	}

	cats.Wait()

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
