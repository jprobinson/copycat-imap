#!/usr/bin/env python
# encoding: utf-8

"""
copycat.py

Created by JP Robinson on 2013-05-06.
"""

import sys
import getopt
import imaplib
import email
from datetime import datetime,timedelta
import time

class CopyCat:
    def __init__(self,creds):
        try:
            self.from_conn = imaplib.IMAP4_SSL(creds['from_host'])
        except:
            log("Unable to connect to 'from' imap server :" + creds['from_host'])
            sys.exit(1) 

        try:
            self.from_conn.login(creds['from_user'],creds['from_pw'])
            log("Logged into IMAP for %s" % (creds['from_user']))
        except:
            log('Unable to authenticate with imap server:'+creds['from_host']+' invalid creds for:'+creds['from_user'])
            sys.exit(1)
        
        if 'gmail' in creds['from_host']:
            # Moving to inbox
            self.from_conn.select('inbox')
        
        try:
            self.to_conn = imaplib.IMAP4_SSL(creds['to_host'])
        except:
            log("Unable to connect to 'to' imap server :" + creds['to_host'])
            sys.exit(1) 

        try:
            self.to_conn.login(creds['to_user'],creds['to_pw'])
            log("Logged into IMAP for %s" % (creds['to_user']))
        except:
            log('Unable to authenticate with imap server:'+creds['to_host']+' invalid creds for:'+creds['to_user'])
            sys.exit(1)

        if 'gmail' in creds['to_host']:
            # Moving to inbox
            self.to_conn.select('inbox') 
    

    def _get_date(self,message):
        date_string = message['Received'].split(';')[1].strip().split(',')[1].strip()[:-12]
        # WHY ADD 3? BC ET ROCKS!!...eh...yeah whatever.
        delta = timedelta(hours=3)
        return time.strptime(date_string,"%d %b %Y %H:%M:%S")
        
    def moveit(self):
        # Finding ALL messages
        typ, msg_data = self.from_conn.search(None,'ALL')
    
        message_nums = msg_data[0].split()
        cnt = int()
        log('pulling %d emails from from inbox' % len(message_nums))
        for count,message_num in enumerate(message_nums):
            cnt = count+1
            typ, raw_message = self.from_conn.fetch(message_num,'(RFC822)')
            message = email.message_from_string(raw_message[0][1])
            print self._get_date(message)
            self.to_conn.append('inbox','',self._get_date(message),raw_message[0][1])
            if count % 100 == 0:
                print "moved %d emails" % (count)
        
        print "COMPLETE! In the end, copycat barfed %d emails into the 'to' email address. Have a nice day" % (count)
        self.from_conn.close()
        self.to_conn.close()


def log(text):
    print '%s - %s' % (datetime.now().strftime('%Y-%m-%d %H:%M:%S'),text)

help_message = '''
Please provide a few tidbits of data to run:
    --from_user         if you
    --from_pw           can't figure
    --from_host         this out. you
    --to_user           shouldn't be
    --to_pw             running this
    --to_host           script.
'''

class Usage(Exception):
    def __init__(self, msg):
        self.msg = msg

OPT_ARGS = ["from_user=", "from_pw=","from_host=","to_user=","to_pw=","to_host="]

def main(argv=None):
    if argv is None:
        argv = sys.argv
    try:
        try:
            opts, args = getopt.getopt(argv[1:], "", OPT_ARGS)
        except getopt.error, msg:
            raise Usage(msg)

        opt_args = [arg[:-1] for arg in OPT_ARGS]
        creds = {}
        for option, value in opts:
          option = option.replace('--','') 
          if option in opt_args:
              creds[option] = value
        
        if len(creds.items()) != 6:
            raise Usage(help_message) 
            

        cat = CopyCat(creds)
        cat.moveit()
        
    except Usage, err:
        print >> sys.stderr, sys.argv[0].split("/")[-1] + ": " + str(err.msg)
        return 2


if __name__ == "__main__":
    sys.exit(main())
