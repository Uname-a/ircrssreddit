package bot

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/martinlindhe/base36"
	"github.com/ugjka/dumbirc"

	"github.com/mmcdole/gofeed"
)

// Client let's you fiddle with http.Client
var Client = &http.Client{}

type bot struct {
	ircNick        string
	ircUser        string
	ircPass        string
	ircServer      string
	ircChannels    []string
	ircTLS         bool
	endpoints      []string
	ircConn        *dumbirc.Connection
	lastID         uint64
	send           chan string
	feed           *gofeed.Parser
	useragent      string
	interval       time.Duration
	round          time.Duration
	printSubreddit bool
}

//Bot settings
type Bot struct {
	IrcNick        string
	IrcUser        string
	IrcPass        string
	IrcServer      string
	IrcChannels    []string
	IrcTLS         bool
	Endpoints      []string
	FetchInterval  time.Duration
	Round          time.Duration
	UserAgent      string
	PrintSubreddit bool
}

//New creates a new bot object
func New(b *Bot) *bot {
	return &bot{
		ircConn:        dumbirc.New(b.IrcNick, b.IrcUser, b.IrcServer, b.IrcTLS),
		ircPass:        b.IrcPass,
		ircChannels:    b.IrcChannels,
		send:           make(chan string, 100),
		feed:           gofeed.NewParser(),
		endpoints:      b.Endpoints,
		useragent:      b.UserAgent,
		interval:       b.FetchInterval,
		round:          b.Round,
		printSubreddit: b.PrintSubreddit,
	}
}

func (b *bot) printer() {
	irc := b.ircConn
	for v := range b.send {
		irc.MsgBulk(b.ircChannels, v)
		time.Sleep(time.Second * 1)
	}
}

func (b *bot) ircControl() {
	irc := b.ircConn
	for {
		select {
		case err := <-irc.Errchan:
			irc.Disconnect()
			log.Println("Irc error", err)
			log.Println("Restarting irc")
			time.Sleep(time.Minute * 1)
			irc.Start()
		}
	}
}

// Get posts
func (b *bot) fetch(endpoint string) (p *gofeed.Feed, err error) {
	if !strings.Contains(endpoint, ".rss") {
		endpoint += ".rss"
	}
	req, err := http.NewRequest("GET", "https://www.reddit.com"+endpoint, nil)
	if err != nil {
		return
	}
	// Headers.
	req.Header.Set("User-Agent", b.useragent)

	resp, err := Client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("fetch response error: " + resp.Status)
	}
	return b.feed.Parse(resp.Body)
}

func (b *bot) firstRun() error {
	for _, v := range b.endpoints {
		posts, err := b.fetch(v)
		if err != nil {
			log.Println("First run", err)
			return err
		}
		for _, v := range posts.Items {
			if !strings.HasPrefix(v.GUID, "t3_") {
				continue
			}
			decoded := base36.Decode(v.GUID[3:])
			if b.lastID < decoded {
				b.lastID = decoded
			}
		}
	}
	return nil
}

func (b *bot) getPosts() {
	var tmpLargest uint64
	dup := make(map[uint64]bool)
	for _, v := range b.endpoints {
		posts, err := b.fetch(v)
		if err != nil {
			log.Println("Could not fetch posts:", err)
			return
		}
		for _, v := range posts.Items {
			if !strings.HasPrefix(v.GUID, "t3_") {
				continue
			}
			decoded := base36.Decode(v.GUID[3:])
			if _, ok := dup[decoded]; ok {
				continue
			}
			dup[decoded] = true
			if tmpLargest < decoded {
				tmpLargest = decoded
			}
			if b.lastID < decoded {

				b.send <- fmt.Sprintf("[%s] %s https://redd.it/%s", v.Title, v.GUID[3:])
			}
		}
	}
	b.lastID = tmpLargest
}

func (b *bot) mainLoop() {
	round := time.Now().Round(b.round)
	if time.Now().After(round) {
		round = round.Add(b.round)
	}
	time.Sleep(round.Sub(time.Now()))
	ticker := time.NewTicker(b.interval)
	b.getPosts()
	for {
		select {
		case <-ticker.C:
			b.getPosts()
		}
	}
}

//Start starts the bot
func (b *bot) Start() {
	if b.ircPass != "" {
		b.ircConn.SetPassword(b.ircPass)
	}
	b.ircConn.HandleJoin(b.ircChannels)
	b.ircConn.HandleNickTaken()
	b.ircConn.HandlePingPong()
	b.ircConn.Start()
	var err error
	for {
		err = b.firstRun()
		if err == nil {
			log.Println("first run succeeded")
			break
		}
		log.Println("first run failed:", err)
		time.Sleep(time.Minute * 10)
		log.Println("retrying first run")
	}
	go b.printer()
	go b.ircControl()
	b.mainLoop()
}
