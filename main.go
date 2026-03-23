package main

import (
	"bytes"
	"encoding/json"
	stderrors "errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	gosort "sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/dustin/go-humanize"
	"github.com/pyed/tailer"
	"github.com/pyed/transmission"
	tgbotapi "gopkg.in/telegram-bot-api.v4"
)

const (
	VERSION = "v1.6"

	HELP = `
	*list* or *li* or *ls*
	Lists all the torrents, takes an optional argument which is a query to list only torrents that has a tracker matches the query, or some of it.

	*head* or *he*
	Lists the first n number of torrents, n defaults to 5 if no argument is provided.

	*tail* or *ta*
	Lists the last n number of torrents, n defaults to 5 if no argument is provided.

	*downs* or *dg*
	Lists torrents with the status of _Downloading_ or in the queue to download.

	*seeding* or *sd*
	Lists torrents with the status of _Seeding_ or in the queue to seed.

	*paused* or *pa*
	Lists _Paused_ torrents.

	*checking* or *ch*
	Lists torrents with the status of _Verifying_ or in the queue to verify.

	*active* or *ac*
	Lists torrents that are actively uploading or downloading.

	*errors* or *er*
	Lists torrents with with errors along with the error message.

	*sort* or *so*
	Manipulate the sorting of the aforementioned commands. Call it without arguments for more.

	*trackers* or *tr*
	Lists all the trackers along with the number of torrents.

	*downloaddir* or *dd*
	Set download directory to the specified path. Transmission will automatically create a
	directory in case you provided an inexistent one.

	*add* or *ad*
	Takes one or many URLs or magnets to add them. You can send a ".torrent" file via Telegram to add it.

	*search* or *se*
	Takes a query and lists torrents with matching names.

	*latest* or *la*
	Lists the newest n torrents, n defaults to 5 if no argument is provided.

	*info* or *in*
	Takes one or more torrent's IDs to list more info about them.
	Use *files* for detailed file-level controls.

	*files* or *fi*
	Show file numbers (index starts at 0), wanted/skip, priority, size and completion.
	Example: *files 12*

	*want* or *wa*
	Mark one torrent's files as wanted. Supports lists/ranges.
	Example: *want 12 1,2,5-8*

	*skip* or *sk*
	Mark one torrent's files as unwanted. Supports lists/ranges.
	Example: *skip 12 0,3-5*

	*fprio* or *fp*
	Set file priority (high|normal|low) for one torrent's files.
	Example: *fprio 12 high 1,2,5-8*

	*startnow* or *sn*
	Start torrents immediately, bypassing queue position.
	Example: *startnow 12 19*

	*qtop*
	Move torrents to top of queue.
	Example: *qtop 12 19*

	*qup*
	Move torrents one step up in queue.
	Example: *qup 12*

	*qdown*
	Move torrents one step down in queue.
	Example: *qdown 12*

	*qbottom* or *qbot*
	Move torrents to bottom of queue.
	Example: *qbottom 12 19*

	*tlimit* or *tl*
	Set per-torrent speed limit in KB/s (not global downlimit/uplimit).
	Example: *tlimit 12 down 2048*

	*tpeers* or *tp*
	Set per-torrent max peers.
	Example: *tpeers 12 60*

	*tratio* or *trr*
	Set per-torrent seed ratio limit.
	Example: *tratio 12 1.5*

	*tidle* or *ti*
	Set per-torrent idle seeding limit (minutes).
	Example: *tidle 12 30*

	*tglobal* or *tg*
	Toggle honoring session-wide limits for a torrent.
	Example: *tglobal 12 on*

	*reannounce* or *ra*
	Force tracker reannounce now.
	Example: *reannounce 12 19*

	*trackerlist* or *tls*
	Show torrent tracker list with tier and announce URL.
	Example: *trackerlist 12*

	*trackerset*
	Replace torrent tracker list. Use blank line between tiers.
	Example:
	*trackerset 12 udp://t1/announce*
	*udp://t2/announce*
	(blank line between tiers)
	*udp://backup/announce*

	*paths* or *pa*
	Show current download dir and torrent file paths for rename discovery.
	Example: *paths 12*

	*move* or *mv*
	Move torrent data to a new location (quote paths with spaces).
	Example: *move 12 "/mnt/media/TV Shows"*

	*rename* or *ren*
	Rename a file/folder path inside one torrent (quote paths with spaces).
	Example: *rename 12 "old folder/file.mkv" "new name.mkv"*

	*stop* or *sp*
	Takes one or more torrent's IDs to stop them, or _all_ to stop all torrents.

	*start* or *st*
	Takes one or more torrent's IDs to start them, or _all_ to start all torrents.

	*check* or *ck*
	Takes one or more torrent's IDs to verify them, or _all_ to verify all torrents.

	*del* or *rm*
	Takes one or more torrent's IDs to delete them.

	*deldata*
	Takes one or more torrent's IDs to delete them and their data.

	*stats* or *sa*
	Shows Transmission's stats.

	*downlimit* or *dl*
	Set global limit for download speed in kilobytes.

	*uplimit* or *ul*
	Set global limit for upload speed in kilobytes.

	*speed* or *ss*
	Shows the upload and download speeds.

	*count* or *co*
	Shows the torrents counts per status.

	*help*
	Shows this help message.

	*version* or *ver*
	Shows version numbers.

	- Prefix commands with '/' if you want to talk to your bot in a group. 
	- report any issues [here](https://github.com/pyed/transmission-telegram)
	`
)

var (

	// flags
	BotToken     string
	Masters      masterSlice
	RPCURL       string
	Username     string
	Password     string
	LogFile      string
	TransLogFile string // Transmission log file
	NoLive       bool
	ProxyURL     string

	// transmission
	Client *transmission.TransmissionClient

	// telegram
	Bot     *tgbotapi.BotAPI
	Updates <-chan tgbotapi.Update

	// chatID will be used to keep track of which chat to send completion notifictions.
	chatID int64

	// logging
	logger = log.New(os.Stdout, "", log.LstdFlags)

	rpcHTTPClient = &http.Client{Timeout: 30 * time.Second}
	rpcSessionID  string
	rpcSessionMu  sync.Mutex

	// interval in seconds for live updates, affects: "active", "info", "speed", "head", "tail"
	interval time.Duration = 5
	// duration controls how many intervals will happen
	duration = 10

	// since telegram's markdown can't be escaped, we have to replace some chars
	// affects only markdown users: info, active, head, tail
	mdReplacer = strings.NewReplacer("*", "•",
		"[", "(",
		"]", ")",
		"_", "-",
		"`", "'")
)

type rpcRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	Params  map[string]any `json:"params,omitempty"`
	Method  string         `json:"method"`
	ID      int64          `json:"id"`
}

type rpcResponse struct {
	JSONRPC   string          `json:"jsonrpc"`
	ResultRaw json.RawMessage `json:"result"`
	Arguments struct {
		Torrents []rpcTorrent `json:"torrents"`
	} `json:"arguments"`
	Result struct {
		Torrents []rpcTorrent `json:"torrents"`
	}
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			ErrorString string `json:"error_string"`
		} `json:"data"`
	} `json:"error"`
}

func (r *rpcResponse) normalize() error {
	if len(r.Arguments.Torrents) > 0 {
		r.Result.Torrents = r.Arguments.Torrents
	}
	if len(r.ResultRaw) == 0 || string(r.ResultRaw) == "null" {
		return nil
	}

	var obj struct {
		Torrents []rpcTorrent `json:"torrents"`
	}
	if err := json.Unmarshal(r.ResultRaw, &obj); err == nil {
		if len(obj.Torrents) > 0 {
			r.Result.Torrents = obj.Torrents
		}
		return nil
	}

	var status string
	if err := json.Unmarshal(r.ResultRaw, &status); err == nil {
		if status != "success" {
			return stderrors.New(status)
		}
	}

	for i := range r.Result.Torrents {
		t := &r.Result.Torrents[i]
		if t.QueuePosition == 0 && t.QueuePositionLegacy != 0 {
			t.QueuePosition = t.QueuePositionLegacy
		}
		if len(t.FileStats) == 0 && len(t.FileStatsLegacy) > 0 {
			t.FileStats = t.FileStatsLegacy
		}
		if t.TrackerList == "" && t.TrackerListLegacy != "" {
			t.TrackerList = t.TrackerListLegacy
		}
		if t.DownloadDir == "" && t.DownloadDirLegacy != "" {
			t.DownloadDir = t.DownloadDirLegacy
		}
		if t.DownloadLimit == 0 && t.DownloadLimitLegacy != 0 {
			t.DownloadLimit = t.DownloadLimitLegacy
		}
		if !t.DownloadLimited && t.DownloadLimitedOld {
			t.DownloadLimited = t.DownloadLimitedOld
		}
		if t.UploadLimit == 0 && t.UploadLimitLegacy != 0 {
			t.UploadLimit = t.UploadLimitLegacy
		}
		if !t.UploadLimited && t.UploadLimitedOld {
			t.UploadLimited = t.UploadLimitedOld
		}
		if t.PeerLimit == 0 && t.PeerLimitLegacy != 0 {
			t.PeerLimit = t.PeerLimitLegacy
		}
		if t.SeedRatioLimit == 0 && t.SeedRatioLimitOld != 0 {
			t.SeedRatioLimit = t.SeedRatioLimitOld
		}
		if t.SeedRatioMode == 0 && t.SeedRatioModeOld != 0 {
			t.SeedRatioMode = t.SeedRatioModeOld
		}
		if t.SeedIdleLimit == 0 && t.SeedIdleLimitOld != 0 {
			t.SeedIdleLimit = t.SeedIdleLimitOld
		}
		if t.SeedIdleMode == 0 && t.SeedIdleModeOld != 0 {
			t.SeedIdleMode = t.SeedIdleModeOld
		}
		if !t.HonorsSessionLimits && t.HonorsSessionOld {
			t.HonorsSessionLimits = t.HonorsSessionOld
		}
		if !t.SequentialDownload && t.SequentialOld {
			t.SequentialDownload = t.SequentialOld
		}
		if t.SequentialFromPiece == 0 && t.SequentialFromOld != 0 {
			t.SequentialFromPiece = t.SequentialFromOld
		}
		for j := range t.Files {
			if t.Files[j].BytesCompleted == 0 && t.Files[j].BytesOld != 0 {
				t.Files[j].BytesCompleted = t.Files[j].BytesOld
			}
			if t.Files[j].BeginPiece == 0 && t.Files[j].BeginOld != 0 {
				t.Files[j].BeginPiece = t.Files[j].BeginOld
			}
			if t.Files[j].EndPiece == 0 && t.Files[j].EndOld != 0 {
				t.Files[j].EndPiece = t.Files[j].EndOld
			}
		}
		for j := range t.FileStats {
			if t.FileStats[j].BytesCompleted == 0 && t.FileStats[j].BytesOld != 0 {
				t.FileStats[j].BytesCompleted = t.FileStats[j].BytesOld
			}
		}
	}

	return nil
}

func toLegacyMethod(method string) string {
	replacer := strings.NewReplacer(
		"torrent_get", "torrent-get",
		"torrent_set", "torrent-set",
		"torrent_start_now", "torrent-start-now",
		"queue_move_top", "queue-move-top",
		"queue_move_up", "queue-move-up",
		"queue_move_down", "queue-move-down",
		"queue_move_bottom", "queue-move-bottom",
		"torrent_reannounce", "torrent-reannounce",
		"torrent_set_location", "torrent-set-location",
		"torrent_rename_path", "torrent-rename-path",
		"session_get", "session-get",
		"session_set", "session-set",
	)
	return replacer.Replace(method)
}

func toLegacyParams(params map[string]any) map[string]any {
	if params == nil {
		return nil
	}
	out := make(map[string]any, len(params))
	for k, v := range params {
		switch k {
		case "fields":
			fields, ok := v.([]string)
			if !ok {
				out[k] = v
				continue
			}
			mapped := make([]string, 0, len(fields))
			for _, f := range fields {
				switch f {
				case "queue_position":
					mapped = append(mapped, "queuePosition")
				case "file_stats":
					mapped = append(mapped, "fileStats")
				case "tracker_list":
					mapped = append(mapped, "trackerList")
				case "download_dir":
					mapped = append(mapped, "downloadDir")
				case "download_limit":
					mapped = append(mapped, "downloadLimit")
				case "download_limited":
					mapped = append(mapped, "downloadLimited")
				case "upload_limit":
					mapped = append(mapped, "uploadLimit")
				case "upload_limited":
					mapped = append(mapped, "uploadLimited")
				case "peer_limit":
					mapped = append(mapped, "peer-limit")
				case "seed_ratio_limit":
					mapped = append(mapped, "seedRatioLimit")
				case "seed_ratio_mode":
					mapped = append(mapped, "seedRatioMode")
				case "seed_idle_limit":
					mapped = append(mapped, "seedIdleLimit")
				case "seed_idle_mode":
					mapped = append(mapped, "seedIdleMode")
				case "honors_session_limits":
					mapped = append(mapped, "honorsSessionLimits")
				default:
					mapped = append(mapped, f)
				}
			}
			out[k] = mapped
		case "files_wanted":
			out["files-wanted"] = v
		case "files_unwanted":
			out["files-unwanted"] = v
		case "priority_high":
			out["priority-high"] = v
		case "priority_normal":
			out["priority-normal"] = v
		case "priority_low":
			out["priority-low"] = v
		case "tracker_list":
			out["trackerList"] = v
		case "download_limit":
			out["downloadLimit"] = v
		case "download_limited":
			out["downloadLimited"] = v
		case "upload_limit":
			out["uploadLimit"] = v
		case "upload_limited":
			out["uploadLimited"] = v
		case "peer_limit":
			out["peer-limit"] = v
		case "seed_ratio_limit":
			out["seedRatioLimit"] = v
		case "seed_ratio_mode":
			out["seedRatioMode"] = v
		case "seed_idle_limit":
			out["seedIdleLimit"] = v
		case "seed_idle_mode":
			out["seedIdleMode"] = v
		case "honors_session_limits":
			out["honorsSessionLimits"] = v
		case "queue_position":
			out["queuePosition"] = v
		default:
			out[k] = v
		}
	}
	return out
}

func shouldFallbackToLegacy(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "method name not recognized") ||
		strings.Contains(msg, "no fields specified")
}

type rpcTorrent struct {
	ID                  int              `json:"id"`
	Name                string           `json:"name"`
	QueuePosition       int              `json:"queue_position"`
	QueuePositionLegacy int              `json:"queuePosition"`
	Files               []rpcTorrentFile `json:"files"`
	FileStats           []rpcFileStat    `json:"file_stats"`
	FileStatsLegacy     []rpcFileStat    `json:"fileStats"`
	Trackers            []rpcTracker     `json:"trackers"`
	TrackerList         string           `json:"tracker_list"`
	TrackerListLegacy   string           `json:"trackerList"`
	DownloadDir         string           `json:"download_dir"`
	DownloadDirLegacy   string           `json:"downloadDir"`
	DownloadLimit       int              `json:"download_limit"`
	DownloadLimitLegacy int              `json:"downloadLimit"`
	DownloadLimited     bool             `json:"download_limited"`
	DownloadLimitedOld  bool             `json:"downloadLimited"`
	UploadLimit         int              `json:"upload_limit"`
	UploadLimitLegacy   int              `json:"uploadLimit"`
	UploadLimited       bool             `json:"upload_limited"`
	UploadLimitedOld    bool             `json:"uploadLimited"`
	PeerLimit           int              `json:"peer_limit"`
	PeerLimitLegacy     int              `json:"peer-limit"`
	SeedRatioLimit      float64          `json:"seed_ratio_limit"`
	SeedRatioLimitOld   float64          `json:"seedRatioLimit"`
	SeedRatioMode       int              `json:"seed_ratio_mode"`
	SeedRatioModeOld    int              `json:"seedRatioMode"`
	SeedIdleLimit       int              `json:"seed_idle_limit"`
	SeedIdleLimitOld    int              `json:"seedIdleLimit"`
	SeedIdleMode        int              `json:"seed_idle_mode"`
	SeedIdleModeOld     int              `json:"seedIdleMode"`
	HonorsSessionLimits bool             `json:"honors_session_limits"`
	HonorsSessionOld    bool             `json:"honorsSessionLimits"`
	SequentialDownload  bool             `json:"sequential_download"`
	SequentialOld       bool             `json:"sequentialDownload"`
	SequentialFromPiece int              `json:"sequential_download_from_piece"`
	SequentialFromOld   int              `json:"sequentialDownloadFromPiece"`
}

type rpcTorrentFile struct {
	BytesCompleted uint64 `json:"bytes_completed"`
	BytesOld       uint64 `json:"bytesCompleted"`
	Length         uint64 `json:"length"`
	Name           string `json:"name"`
	BeginPiece     int    `json:"begin_piece"`
	BeginOld       int    `json:"beginPiece"`
	EndPiece       int    `json:"end_piece"`
	EndOld         int    `json:"endPiece"`
}

type rpcFileStat struct {
	BytesCompleted uint64 `json:"bytes_completed"`
	BytesOld       uint64 `json:"bytesCompleted"`
	Wanted         bool   `json:"wanted"`
	Priority       int    `json:"priority"`
}

type rpcTracker struct {
	ID       int    `json:"id"`
	Tier     int    `json:"tier"`
	Announce string `json:"announce"`
}

// we need a type for masters for the flag package to parse them as a slice
type masterSlice []string

// String is mandatory functions for the flag package
func (masters *masterSlice) String() string {
	return fmt.Sprintf("%s", *masters)
}

// Set is mandatory functions for the flag package
func (masters *masterSlice) Set(master string) error {
	*masters = append(*masters, strings.ToLower(master))
	return nil
}

// Contains takes a string and return true of masterSlice has it
func (masters masterSlice) Contains(master string) bool {
	master = strings.ToLower(master)
	for i := range masters {
		if masters[i] == master {
			return true
		}
	}
	return false
}

// init flags
func init() {
	// define arguments and parse them.
	flag.StringVar(&BotToken, "token", "", "Telegram bot token, Can be passed via environment variable 'TT_BOTT'")
	flag.Var(&Masters, "master", "Your telegram handler, So the bot will only respond to you. Can specify more than one")
	flag.StringVar(&RPCURL, "url", "http://localhost:9091/transmission/rpc", "Transmission RPC URL")
	flag.StringVar(&Username, "username", "", "Transmission username")
	flag.StringVar(&Password, "password", "", "Transmission password")
	flag.StringVar(&LogFile, "logfile", "", "Send logs to a file")
	flag.StringVar(&TransLogFile, "transmission-logfile", "", "Open transmission logfile to monitor torrents completion")
	flag.BoolVar(&NoLive, "no-live", false, "Don't edit and update info after sending")
	flag.StringVar(&ProxyURL, "proxy", "", "SOCKS5 proxy URL (e.g., socks5://user:pass@host:port). Can be passed via TT_PROXY env var")

	// set the usage message
	flag.Usage = func() {
		fmt.Fprint(os.Stderr, "Usage: transmission-telegram <-token=TOKEN> <-master=@tuser> [-master=@yuser2] [-url=http://] [-username=user] [-password=pass] [-proxy=socks5://...]\n\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	// check environment variables for optional settings
	if ProxyURL == "" {
		ProxyURL = os.Getenv("TT_PROXY")
	}

	// if we don't have BotToken passed, check the environment variable "TT_BOTT"
	if BotToken == "" {
		if token := os.Getenv("TT_BOTT"); len(token) > 1 {
			BotToken = token
		}
	}

	// make sure that we have the two madatory arguments: telegram token & master's handler.
	if BotToken == "" ||
		len(Masters) < 1 {
		fmt.Fprintf(os.Stderr, "Error: Mandatory argument missing! (-token or -master)\n\n")
		flag.Usage()
		os.Exit(1)
	}

	// make sure that the handler doesn't contain @
	for i := range Masters {
		Masters[i] = strings.Replace(Masters[i], "@", "", -1)
	}

	// if we got a log file, log to it
	if LogFile != "" {
		logf, err := os.OpenFile(LogFile, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
		if err != nil {
			log.Fatal(err)
		}
		logger.SetOutput(logf)
	}

	// if we got a transmission log file, monitor it for torrents completion to notify upon them.
	if TransLogFile != "" {
		go func() {
			ft := tailer.RunFileTailer(TransLogFile, false, nil)

			// [2017-02-22 21:00:00.898] File-Name State changed from "Incomplete" to "Complete" (torrent.c:2218)
			const (
				substring = `"Incomplete" to "Complete"`
				start     = len(`[2017-02-22 21:00:00.898] `)
				end       = len(` State changed from "Incomplete" to "Complete" (torrent.c:2218)`)
			)

			for {
				select {
				case line := <-ft.Lines():
					if strings.Contains(line, substring) {
						// if we don't have a chatID continue
						if chatID == 0 {
							continue
						}

						msg := fmt.Sprintf("Completed: %s", line[start:len(line)-end])
						send(msg, chatID, false)
					}
				case err := <-ft.Errors():
					logger.Printf("[ERROR] tailing transmission log: %s", err)
					return
				}

			}
		}()
	}

	// if the `-username` flag isn't set, look into the environment variable 'TR_AUTH'
	if Username == "" {
		if values := strings.Split(os.Getenv("TR_AUTH"), ":"); len(values) > 1 {
			Username, Password = values[0], values[1]
		}
	}

	// log the flags
	logger.Printf("[INFO] Token=%s\n\t\tMasters=%s\n\t\tURL=%s\n\t\tUSER=%s\n\t\tPASS=%s\n\t\tPROXY=%s",
		BotToken, Masters, RPCURL, Username, Password, ProxyURL)
}

// init transmission
func init() {
	var err error
	Client, err = transmission.New(RPCURL, Username, Password)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Transmission: Make sure you have the right URL, Username and Password\n")
		os.Exit(1)
	}

}

// init telegram
func init() {
	// authorize using the token
	var err error
	botClient := &http.Client{Timeout: 30 * time.Second}
	if ProxyURL != "" {
		proxyURL, err := url.Parse(ProxyURL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[ERROR] Proxy: failed to parse proxy URL: %s\n", err)
			os.Exit(1)
		}

		transport := &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		}
		botClient = &http.Client{
			Transport: transport,
			Timeout:   30 * time.Second,
		}
		logger.Printf("[INFO] Telegram proxy configured: %s", ProxyURL)
	}

	Bot, err = tgbotapi.NewBotAPIWithClient(BotToken, botClient)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Telegram: %s\n", err)
		os.Exit(1)
	}

	logger.Printf("[INFO] Authorized: %s", Bot.Self.UserName)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	Updates, err = Bot.GetUpdatesChan(u)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Telegram: %s\n", err)
		os.Exit(1)
	}
}

func main() {
	for update := range Updates {
		// ignore edited messages
		if update.Message == nil {
			continue
		}

		// ignore non masters
		if !Masters.Contains(update.Message.From.UserName) {
			logger.Printf("[INFO] Ignored a message from: %s", update.Message.From.String())
			continue
		}

		// update chatID for complete notification
		if TransLogFile != "" && chatID != update.Message.Chat.ID {
			chatID = update.Message.Chat.ID
		}

		// tokenize the update
		tokens := strings.Split(update.Message.Text, " ")

		// preprocess message based on URL schema
		// in case those were added from the mobile via "Share..." option
		// when it is not possible to easily prepend it with "add" command
		if strings.HasPrefix(tokens[0], "magnet") || strings.HasPrefix(tokens[0], "http") {
			tokens = append([]string{"add"}, tokens...)
		}

		command := strings.ToLower(tokens[0])

		switch command {
		case "list", "/list", "li", "/li", "/ls", "ls":
			go list(update, tokens[1:])

		case "head", "/head", "he", "/he":
			go head(update, tokens[1:])

		case "tail", "/tail", "ta", "/ta":
			go tail(update, tokens[1:])

		case "downs", "/downs", "dg", "/dg":
			go downs(update)

		case "seeding", "/seeding", "sd", "/sd":
			go seeding(update)

		case "paused", "/paused", "/pa":
			go paused(update)

		case "checking", "/checking", "ch", "/ch":
			go checking(update)

		case "active", "/active", "ac", "/ac":
			go active(update)

		case "errors", "/errors", "er", "/er":
			go errors(update)

		case "sort", "/sort", "so", "/so":
			go sort(update, tokens[1:])

		case "trackers", "/trackers", "tr", "/tr":
			go trackers(update)

		case "downloaddir", "dd":
			go downloaddir(update, tokens[1:])

		case "add", "/add", "ad", "/ad":
			go add(update, tokens[1:])

		case "search", "/search", "se", "/se":
			go search(update, tokens[1:])

		case "latest", "/latest", "la", "/la":
			go latest(update, tokens[1:])

		case "info", "/info", "in", "/in":
			go info(update, tokens[1:])

		case "files", "/files", "fi", "/fi":
			go files(update, tokens[1:])

		case "want", "/want", "wa", "/wa":
			go want(update, tokens[1:])

		case "skip", "/skip", "sk", "/sk":
			go skip(update, tokens[1:])

		case "fprio", "/fprio", "fp", "/fp":
			go fprio(update, tokens[1:])

		case "startnow", "/startnow", "sn", "/sn":
			go queueAction(update, "startnow", "torrent_start_now", tokens[1:])

		case "qtop", "/qtop":
			go queueAction(update, "qtop", "queue_move_top", tokens[1:])

		case "qup", "/qup":
			go queueAction(update, "qup", "queue_move_up", tokens[1:])

		case "qdown", "/qdown":
			go queueAction(update, "qdown", "queue_move_down", tokens[1:])

		case "qbottom", "/qbottom", "qbot", "/qbot":
			go queueAction(update, "qbottom", "queue_move_bottom", tokens[1:])

		case "tlimit", "/tlimit", "tl", "/tl":
			go tlimit(update, tokens[1:])

		case "tpeers", "/tpeers", "tp", "/tp":
			go tpeers(update, tokens[1:])

		case "tratio", "/tratio", "trr", "/trr":
			go tratio(update, tokens[1:])

		case "tidle", "/tidle", "ti", "/ti":
			go tidle(update, tokens[1:])

		case "tglobal", "/tglobal", "tg", "/tg":
			go tglobal(update, tokens[1:])

		case "reannounce", "/reannounce", "ra", "/ra":
			go queueAction(update, "reannounce", "torrent_reannounce", tokens[1:])

		case "trackerlist", "/trackerlist", "tls", "/tls":
			go trackerlist(update, tokens[1:])

		case "trackerset", "/trackerset":
			go trackerset(update, tokens[1:])

		case "paths", "/paths", "pa":
			go paths(update, tokens[1:])

		case "move", "/move", "mv", "/mv":
			go move(update)

		case "rename", "/rename", "ren", "/ren":
			go rename(update)

		case "stop", "/stop", "sp", "/sp":
			go stop(update, tokens[1:])

		case "start", "/start", "st", "/st":
			go start(update, tokens[1:])

		case "check", "/check", "ck", "/ck":
			go check(update, tokens[1:])

		case "stats", "/stats", "sa", "/sa":
			go stats(update)

		case "downlimit", "dl":
			go downlimit(update, tokens[1:])

		case "uplimit", "ul":
			go uplimit(update, tokens[1:])

		case "speed", "/speed", "ss", "/ss":
			go speed(update)

		case "count", "/count", "co", "/co":
			go count(update)

		case "del", "/del", "rm", "/rm":
			go del(update, tokens[1:])

		case "deldata", "/deldata":
			go deldata(update, tokens[1:])

		case "help", "/help":
			go sendHelp(update.Message.Chat.ID)

		case "version", "/version", "ver", "/ver":
			go getVersion(update)

		case "":
			// might be a file received
			go receiveTorrent(update)

		default:
			// no such command, try help
			go send("No such command, try /help", update.Message.Chat.ID, false)

		}
	}
}

func sendHelp(chatID int64) {
	const stopSection = "\n\t*stop* or *sp*"
	idx := strings.Index(HELP, stopSection)
	if idx < 0 {
		send(HELP, chatID, true)
		return
	}
	send(HELP[:idx], chatID, true)
	send(HELP[idx:], chatID, true)
}

func rpcCall(method string, params map[string]any) (*rpcResponse, error) {
	call := func(callMethod string, callParams map[string]any, legacy bool) (*rpcResponse, error) {
		var body []byte
		var err error
		if legacy {
			reqBody := map[string]any{
				"method":    callMethod,
				"arguments": callParams,
			}
			body, err = json.Marshal(reqBody)
		} else {
			reqBody := rpcRequest{
				JSONRPC: "2.0",
				Method:  callMethod,
				Params:  callParams,
				ID:      time.Now().UnixNano(),
			}
			body, err = json.Marshal(reqBody)
		}
		if err != nil {
			return nil, err
		}

		for try := 0; try < 2; try++ {
			req, err := http.NewRequest(http.MethodPost, RPCURL, bytes.NewReader(body))
			if err != nil {
				return nil, err
			}
			req.Header.Set("Content-Type", "application/json")
			if Username != "" || Password != "" {
				req.SetBasicAuth(Username, Password)
			}

			rpcSessionMu.Lock()
			currentSessionID := rpcSessionID
			rpcSessionMu.Unlock()
			if currentSessionID != "" {
				req.Header.Set("X-Transmission-Session-Id", currentSessionID)
			}

			resp, err := rpcHTTPClient.Do(req)
			if err != nil {
				return nil, err
			}
			if resp.StatusCode == http.StatusConflict {
				newSessionID := resp.Header.Get("X-Transmission-Session-Id")
				resp.Body.Close()
				if newSessionID == "" {
					return nil, fmt.Errorf("rpc 409 without session id")
				}
				rpcSessionMu.Lock()
				rpcSessionID = newSessionID
				rpcSessionMu.Unlock()
				continue
			}

			raw, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				return nil, err
			}

			out := &rpcResponse{}
			if err := json.Unmarshal(raw, out); err != nil {
				return nil, err
			}
			if out.Error != nil {
				if out.Error.Data.ErrorString != "" {
					return nil, fmt.Errorf("%s: %s", out.Error.Message, out.Error.Data.ErrorString)
				}
				return nil, fmt.Errorf("%s", out.Error.Message)
			}
			if err := out.normalize(); err != nil {
				return nil, err
			}
			return out, nil
		}
		return nil, fmt.Errorf("rpc request failed after session retry")
	}

	out, err := call(method, params, false)
	if shouldFallbackToLegacy(err) {
		return call(toLegacyMethod(method), toLegacyParams(params), true)
	}
	return out, err
}

func parseIntToken(token string) (int, error) {
	v, err := strconv.Atoi(token)
	if err != nil {
		return 0, fmt.Errorf("%q is not a number", token)
	}
	return v, nil
}

func parseFileSelection(tokens []string) ([]int, error) {
	joined := strings.Join(tokens, ",")
	if joined == "" {
		return nil, fmt.Errorf("no file numbers provided")
	}
	chunks := strings.Split(joined, ",")
	seen := make(map[int]struct{})
	files := make([]int, 0, len(chunks))
	for _, chunk := range chunks {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}
		if strings.Contains(chunk, "-") {
			bits := strings.SplitN(chunk, "-", 2)
			if len(bits) != 2 {
				return nil, fmt.Errorf("invalid range %q", chunk)
			}
			start, err := parseIntToken(bits[0])
			if err != nil {
				return nil, err
			}
			end, err := parseIntToken(bits[1])
			if err != nil {
				return nil, err
			}
			if start > end {
				return nil, fmt.Errorf("invalid range %q", chunk)
			}
			for i := start; i <= end; i++ {
				if _, ok := seen[i]; ok {
					continue
				}
				seen[i] = struct{}{}
				files = append(files, i)
			}
			continue
		}
		idx, err := parseIntToken(chunk)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[idx]; ok {
			continue
		}
		seen[idx] = struct{}{}
		files = append(files, idx)
	}
	gosort.Ints(files)
	return files, nil
}

func validateFileBounds(files []int, max int) error {
	for _, f := range files {
		if f < 0 || f >= max {
			return fmt.Errorf("file %d is out of bounds (0-%d)", f, max-1)
		}
	}
	return nil
}

func priorityName(p int) string {
	switch p {
	case -1:
		return "low"
	case 0:
		return "normal"
	case 1:
		return "high"
	default:
		return fmt.Sprintf("unknown(%d)", p)
	}
}

func parseTorrentIDs(tokens []string) ([]int, error) {
	if len(tokens) == 0 {
		return nil, fmt.Errorf("missing torrent id(s)")
	}
	ids := make([]int, 0, len(tokens))
	for _, tok := range tokens {
		id, err := parseIntToken(tok)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func commandTail(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	for i := 0; i < len(text); i++ {
		if text[i] == ' ' || text[i] == '\n' || text[i] == '\t' {
			return strings.TrimSpace(text[i+1:])
		}
	}
	return ""
}

func splitCommandArgs(text string) []string {
	args := make([]string, 0)
	var current strings.Builder
	var quote rune
	for _, r := range text {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
				continue
			}
			current.WriteRune(r)
		case r == '"' || r == '\'':
			quote = r
		case r == ' ' || r == '\n' || r == '\t':
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}
	return args
}

// list will form and send a list of all the torrents
// takes an optional argument which is a query to match against trackers
// to list only torrents that has a tracker that matchs.
func list(ud tgbotapi.Update, tokens []string) {
	torrents, err := Client.GetTorrents()
	if err != nil {
		send("*list:* "+err.Error(), ud.Message.Chat.ID, false)
		return
	}

	buf := new(bytes.Buffer)
	// if it gets a query, it will list torrents that has trackers that match the query
	if len(tokens) != 0 {
		// (?i) for case insensitivity
		regx, err := regexp.Compile("(?i)" + tokens[0])
		if err != nil {
			send("*list:* "+err.Error(), ud.Message.Chat.ID, false)
			return
		}

		for i := range torrents {
			if regx.MatchString(torrents[i].GetTrackers()) {
				buf.WriteString(fmt.Sprintf("<%d> %s\n", torrents[i].ID, torrents[i].Name))
			}
		}
	} else { // if we did not get a query, list all torrents
		for i := range torrents {
			buf.WriteString(fmt.Sprintf("<%d> %s\n", torrents[i].ID, torrents[i].Name))
		}
	}

	if buf.Len() == 0 {
		// if we got a tracker query show different message
		if len(tokens) != 0 {
			send(fmt.Sprintf("*list:* No tracker matches: *%s*", tokens[0]), ud.Message.Chat.ID, true)
			return
		}
		send("*list:* no torrents", ud.Message.Chat.ID, false)
		return
	}

	send(buf.String(), ud.Message.Chat.ID, false)
}

// head will list the first 5 or n torrents
func head(ud tgbotapi.Update, tokens []string) {
	var (
		n   = 5 // default to 5
		err error
	)

	if len(tokens) > 0 {
		n, err = strconv.Atoi(tokens[0])
		if err != nil {
			send("*head:* argument must be a number", ud.Message.Chat.ID, false)
			return
		}
	}

	torrents, err := Client.GetTorrents()
	if err != nil {
		send("*head:* "+err.Error(), ud.Message.Chat.ID, false)
		return
	}

	// make sure that we stay in the boundaries
	if n <= 0 || n > len(torrents) {
		n = len(torrents)
	}

	buf := new(bytes.Buffer)
	for i := range torrents[:n] {
		torrentName := mdReplacer.Replace(torrents[i].Name) // escape markdown
		buf.WriteString(fmt.Sprintf("`<%d>` *%s*\n%s *%s* of *%s* (*%.1f%%*) ↓ *%s*  ↑ *%s* R: *%s*\n\n",
			torrents[i].ID, torrentName, torrents[i].TorrentStatus(), humanize.Bytes(torrents[i].Have()),
			humanize.Bytes(torrents[i].SizeWhenDone), torrents[i].PercentDone*100, humanize.Bytes(torrents[i].RateDownload),
			humanize.Bytes(torrents[i].RateUpload), torrents[i].Ratio()))
	}

	if buf.Len() == 0 {
		send("*head:* no torrents", ud.Message.Chat.ID, false)
		return
	}

	msgID := send(buf.String(), ud.Message.Chat.ID, true)

	if NoLive {
		return
	}

	// keep the info live
	for i := 0; i < duration; i++ {
		time.Sleep(time.Second * interval)
		buf.Reset()

		torrents, err = Client.GetTorrents()
		if err != nil {
			continue // try again if some error heppened
		}

		if len(torrents) < 1 {
			continue
		}

		// make sure that we stay in the boundaries
		if n <= 0 || n > len(torrents) {
			n = len(torrents)
		}

		for _, torrent := range torrents[:n] {
			torrentName := mdReplacer.Replace(torrent.Name) // escape markdown
			buf.WriteString(fmt.Sprintf("`<%d>` *%s*\n%s *%s* of *%s* (*%.1f%%*) ↓ *%s*  ↑ *%s* R: *%s*\n\n",
				torrent.ID, torrentName, torrent.TorrentStatus(), humanize.Bytes(torrent.Have()),
				humanize.Bytes(torrent.SizeWhenDone), torrent.PercentDone*100, humanize.Bytes(torrent.RateDownload),
				humanize.Bytes(torrent.RateUpload), torrent.Ratio()))
		}

		// no need to check if it is empty, as if the buffer is empty telegram won't change the message
		editConf := tgbotapi.NewEditMessageText(ud.Message.Chat.ID, msgID, buf.String())
		editConf.ParseMode = tgbotapi.ModeMarkdown
		Bot.Send(editConf)
	}

}

// tail lists the last 5 or n torrents
func tail(ud tgbotapi.Update, tokens []string) {
	var (
		n   = 5 // default to 5
		err error
	)

	if len(tokens) > 0 {
		n, err = strconv.Atoi(tokens[0])
		if err != nil {
			send("*tail:* argument must be a number", ud.Message.Chat.ID, false)
			return
		}
	}

	torrents, err := Client.GetTorrents()
	if err != nil {
		send("*tail:* "+err.Error(), ud.Message.Chat.ID, false)
		return
	}

	// make sure that we stay in the boundaries
	if n <= 0 || n > len(torrents) {
		n = len(torrents)
	}

	buf := new(bytes.Buffer)
	for _, torrent := range torrents[len(torrents)-n:] {
		torrentName := mdReplacer.Replace(torrent.Name) // escape markdown
		buf.WriteString(fmt.Sprintf("`<%d>` *%s*\n%s *%s* of *%s* (*%.1f%%*) ↓ *%s*  ↑ *%s* R: *%s*\n\n",
			torrent.ID, torrentName, torrent.TorrentStatus(), humanize.Bytes(torrent.Have()),
			humanize.Bytes(torrent.SizeWhenDone), torrent.PercentDone*100, humanize.Bytes(torrent.RateDownload),
			humanize.Bytes(torrent.RateUpload), torrent.Ratio()))
	}

	if buf.Len() == 0 {
		send("*tail:* no torrents", ud.Message.Chat.ID, false)
		return
	}

	msgID := send(buf.String(), ud.Message.Chat.ID, true)

	if NoLive {
		return
	}

	// keep the info live
	for i := 0; i < duration; i++ {
		time.Sleep(time.Second * interval)
		buf.Reset()

		torrents, err = Client.GetTorrents()
		if err != nil {
			continue // try again if some error heppened
		}

		if len(torrents) < 1 {
			continue
		}

		// make sure that we stay in the boundaries
		if n <= 0 || n > len(torrents) {
			n = len(torrents)
		}

		for _, torrent := range torrents[len(torrents)-n:] {
			torrentName := mdReplacer.Replace(torrent.Name) // escape markdown
			buf.WriteString(fmt.Sprintf("`<%d>` *%s*\n%s *%s* of *%s* (*%.1f%%*) ↓ *%s*  ↑ *%s* R: *%s*\n\n",
				torrent.ID, torrentName, torrent.TorrentStatus(), humanize.Bytes(torrent.Have()),
				humanize.Bytes(torrent.SizeWhenDone), torrent.PercentDone*100, humanize.Bytes(torrent.RateDownload),
				humanize.Bytes(torrent.RateUpload), torrent.Ratio()))
		}

		// no need to check if it is empty, as if the buffer is empty telegram won't change the message
		editConf := tgbotapi.NewEditMessageText(ud.Message.Chat.ID, msgID, buf.String())
		editConf.ParseMode = tgbotapi.ModeMarkdown
		Bot.Send(editConf)
	}

}

// downs will send the names of torrents with status 'Downloading' or in queue to
func downs(ud tgbotapi.Update) {
	torrents, err := Client.GetTorrents()
	if err != nil {
		send("*downs:* "+err.Error(), ud.Message.Chat.ID, false)
		return
	}

	buf := new(bytes.Buffer)
	for i := range torrents {
		// Downloading or in queue to download
		if torrents[i].Status == transmission.StatusDownloading ||
			torrents[i].Status == transmission.StatusDownloadPending {
			buf.WriteString(fmt.Sprintf("<%d> %s\n", torrents[i].ID, torrents[i].Name))
		}
	}

	if buf.Len() == 0 {
		send("No downloads", ud.Message.Chat.ID, false)
		return
	}
	send(buf.String(), ud.Message.Chat.ID, false)
}

// seeding will send the names of the torrents with the status 'Seeding' or in the queue to
func seeding(ud tgbotapi.Update) {
	torrents, err := Client.GetTorrents()
	if err != nil {
		send("*seeding:* "+err.Error(), ud.Message.Chat.ID, false)
		return
	}

	buf := new(bytes.Buffer)
	for i := range torrents {
		if torrents[i].Status == transmission.StatusSeeding ||
			torrents[i].Status == transmission.StatusSeedPending {
			buf.WriteString(fmt.Sprintf("<%d> %s\n", torrents[i].ID, torrents[i].Name))
		}
	}

	if buf.Len() == 0 {
		send("No torrents seeding", ud.Message.Chat.ID, false)
		return
	}

	send(buf.String(), ud.Message.Chat.ID, false)

}

// paused will send the names of the torrents with status 'Paused'
func paused(ud tgbotapi.Update) {
	torrents, err := Client.GetTorrents()
	if err != nil {
		send("*paused:* "+err.Error(), ud.Message.Chat.ID, false)
		return
	}

	buf := new(bytes.Buffer)
	for i := range torrents {
		if torrents[i].Status == transmission.StatusStopped {
			buf.WriteString(fmt.Sprintf("<%d> %s\n%s (%.1f%%) DL: %s UL: %s  R: %s\n\n",
				torrents[i].ID, torrents[i].Name, torrents[i].TorrentStatus(),
				torrents[i].PercentDone*100, humanize.Bytes(torrents[i].DownloadedEver),
				humanize.Bytes(torrents[i].UploadedEver), torrents[i].Ratio()))
		}
	}

	if buf.Len() == 0 {
		send("No paused torrents", ud.Message.Chat.ID, false)
		return
	}

	send(buf.String(), ud.Message.Chat.ID, false)
}

// checking will send the names of torrents with the status 'verifying' or in the queue to
func checking(ud tgbotapi.Update) {
	torrents, err := Client.GetTorrents()
	if err != nil {
		send("*checking:* "+err.Error(), ud.Message.Chat.ID, false)
		return
	}

	buf := new(bytes.Buffer)
	for i := range torrents {
		if torrents[i].Status == transmission.StatusChecking ||
			torrents[i].Status == transmission.StatusCheckPending {
			buf.WriteString(fmt.Sprintf("<%d> %s\n%s (%.1f%%)\n\n",
				torrents[i].ID, torrents[i].Name, torrents[i].TorrentStatus(),
				torrents[i].PercentDone*100))

		}
	}

	if buf.Len() == 0 {
		send("No torrents verifying", ud.Message.Chat.ID, false)
		return
	}

	send(buf.String(), ud.Message.Chat.ID, false)
}

// active will send torrents that are actively downloading or uploading
func active(ud tgbotapi.Update) {
	torrents, err := Client.GetTorrents()
	if err != nil {
		send("*active:* "+err.Error(), ud.Message.Chat.ID, false)
		return
	}

	buf := new(bytes.Buffer)
	for i := range torrents {
		if torrents[i].RateDownload > 0 ||
			torrents[i].RateUpload > 0 {
			// escape markdown
			torrentName := mdReplacer.Replace(torrents[i].Name)
			buf.WriteString(fmt.Sprintf("`<%d>` *%s*\n%s *%s* of *%s* (*%.1f%%*) ↓ *%s*  ↑ *%s* R: *%s*\n\n",
				torrents[i].ID, torrentName, torrents[i].TorrentStatus(), humanize.Bytes(torrents[i].Have()),
				humanize.Bytes(torrents[i].SizeWhenDone), torrents[i].PercentDone*100, humanize.Bytes(torrents[i].RateDownload),
				humanize.Bytes(torrents[i].RateUpload), torrents[i].Ratio()))
		}
	}
	if buf.Len() == 0 {
		send("No active torrents", ud.Message.Chat.ID, false)
		return
	}

	msgID := send(buf.String(), ud.Message.Chat.ID, true)

	if NoLive {
		return
	}

	// keep the active list live for 'duration * interval'
	for i := 0; i < duration; i++ {
		time.Sleep(time.Second * interval)
		// reset the buffer to reuse it
		buf.Reset()

		// update torrents
		torrents, err = Client.GetTorrents()
		if err != nil {
			continue // if there was error getting torrents, skip to the next iteration
		}

		// do the same loop again
		for i := range torrents {
			if torrents[i].RateDownload > 0 ||
				torrents[i].RateUpload > 0 {
				torrentName := mdReplacer.Replace(torrents[i].Name) // replace markdown chars
				buf.WriteString(fmt.Sprintf("`<%d>` *%s*\n%s *%s* of *%s* (*%.1f%%*) ↓ *%s*  ↑ *%s* R: *%s*\n\n",
					torrents[i].ID, torrentName, torrents[i].TorrentStatus(), humanize.Bytes(torrents[i].Have()),
					humanize.Bytes(torrents[i].SizeWhenDone), torrents[i].PercentDone*100, humanize.Bytes(torrents[i].RateDownload),
					humanize.Bytes(torrents[i].RateUpload), torrents[i].Ratio()))
			}
		}

		// no need to check if it is empty, as if the buffer is empty telegram won't change the message
		editConf := tgbotapi.NewEditMessageText(ud.Message.Chat.ID, msgID, buf.String())
		editConf.ParseMode = tgbotapi.ModeMarkdown
		Bot.Send(editConf)
	}
	// sleep one more time before putting the dashes
	time.Sleep(time.Second * interval)

	// replace the speed with dashes to indicate that we are done being live
	buf.Reset()
	for i := range torrents {
		if torrents[i].RateDownload > 0 ||
			torrents[i].RateUpload > 0 {
			// escape markdown
			torrentName := mdReplacer.Replace(torrents[i].Name)
			buf.WriteString(fmt.Sprintf("`<%d>` *%s*\n%s *%s* of *%s* (*%.1f%%*) ↓ *-*  ↑ *-* R: *%s*\n\n",
				torrents[i].ID, torrentName, torrents[i].TorrentStatus(), humanize.Bytes(torrents[i].Have()),
				humanize.Bytes(torrents[i].SizeWhenDone), torrents[i].PercentDone*100, torrents[i].Ratio()))
		}
	}

	editConf := tgbotapi.NewEditMessageText(ud.Message.Chat.ID, msgID, buf.String())
	editConf.ParseMode = tgbotapi.ModeMarkdown
	Bot.Send(editConf)

}

// errors will send torrents with errors
func errors(ud tgbotapi.Update) {
	torrents, err := Client.GetTorrents()
	if err != nil {
		send("*errors:* "+err.Error(), ud.Message.Chat.ID, false)
		return
	}

	buf := new(bytes.Buffer)
	for i := range torrents {
		if torrents[i].Error != 0 {
			buf.WriteString(fmt.Sprintf("<%d> %s\n%s\n",
				torrents[i].ID, torrents[i].Name, torrents[i].ErrorString))
		}
	}
	if buf.Len() == 0 {
		send("No errors", ud.Message.Chat.ID, false)
		return
	}
	send(buf.String(), ud.Message.Chat.ID, false)
}

// sort changes torrents sorting
func sort(ud tgbotapi.Update, tokens []string) {
	if len(tokens) == 0 {
		send(`*sort* takes one of:
			(*id, name, age, size, progress, downspeed, upspeed, download, upload, ratio*)
			optionally start with (*rev*) for reversed order
			e.g. "*sort rev size*" to get biggest torrents first.`, ud.Message.Chat.ID, true)
		return
	}

	var reversed bool
	if strings.ToLower(tokens[0]) == "rev" {
		reversed = true
		tokens = tokens[1:]
	}

	switch strings.ToLower(tokens[0]) {
	case "id":
		if reversed {
			Client.SetSort(transmission.SortRevID)
			break
		}
		Client.SetSort(transmission.SortID)
	case "name":
		if reversed {
			Client.SetSort(transmission.SortRevName)
			break
		}
		Client.SetSort(transmission.SortName)
	case "age":
		if reversed {
			Client.SetSort(transmission.SortRevAge)
			break
		}
		Client.SetSort(transmission.SortAge)
	case "size":
		if reversed {
			Client.SetSort(transmission.SortRevSize)
			break
		}
		Client.SetSort(transmission.SortSize)
	case "progress":
		if reversed {
			Client.SetSort(transmission.SortRevProgress)
			break
		}
		Client.SetSort(transmission.SortProgress)
	case "downspeed":
		if reversed {
			Client.SetSort(transmission.SortRevDownSpeed)
			break
		}
		Client.SetSort(transmission.SortDownSpeed)
	case "upspeed":
		if reversed {
			Client.SetSort(transmission.SortRevUpSpeed)
			break
		}
		Client.SetSort(transmission.SortUpSpeed)
	case "download":
		if reversed {
			Client.SetSort(transmission.SortRevDownloaded)
			break
		}
		Client.SetSort(transmission.SortDownloaded)
	case "upload":
		if reversed {
			Client.SetSort(transmission.SortRevUploaded)
			break
		}
		Client.SetSort(transmission.SortUploaded)
	case "ratio":
		if reversed {
			Client.SetSort(transmission.SortRevRatio)
			break
		}
		Client.SetSort(transmission.SortRatio)
	default:
		send("unkown sorting method", ud.Message.Chat.ID, false)
		return
	}

	if reversed {
		send("*sort:* reversed "+tokens[0], ud.Message.Chat.ID, false)
		return
	}
	send("*sort:* "+tokens[0], ud.Message.Chat.ID, false)
}

var trackerRegex = regexp.MustCompile(`[https?|udp]://([^:/]*)`)

// trackers will send a list of trackers and how many torrents each one has
func trackers(ud tgbotapi.Update) {
	torrents, err := Client.GetTorrents()
	if err != nil {
		send("*trackers:* "+err.Error(), ud.Message.Chat.ID, false)
		return
	}

	trackers := make(map[string]int)

	for i := range torrents {
		for _, tracker := range torrents[i].Trackers {
			sm := trackerRegex.FindSubmatch([]byte(tracker.Announce))
			if len(sm) > 1 {
				currentTracker := string(sm[1])
				n, ok := trackers[currentTracker]
				if !ok {
					trackers[currentTracker] = 1
					continue
				}
				trackers[currentTracker] = n + 1
			}
		}
	}

	buf := new(bytes.Buffer)
	for k, v := range trackers {
		buf.WriteString(fmt.Sprintf("%d - %s\n", v, k))
	}

	if buf.Len() == 0 {
		send("No trackers!", ud.Message.Chat.ID, false)
		return
	}
	send(buf.String(), ud.Message.Chat.ID, false)
}

// downloaddir takes a path and sets it as the download directory
func downloaddir(ud tgbotapi.Update, tokens []string) {
	if len(tokens) < 1 {
		send("Please, specify a path for downloaddir", ud.Message.Chat.ID, false)
		return
	}

	downloadDir := tokens[0]

	cmd := transmission.NewSessionSetCommand()
	cmd.SetDownloadDir(downloadDir)

	out, err := Client.ExecuteCommand(cmd)
	if err != nil {
		send("*downloaddir:* "+err.Error(), ud.Message.Chat.ID, false)
		return
	}
	if out.Result != "success" {
		send("*downloaddir:* "+out.Result, ud.Message.Chat.ID, false)
		return
	}

	send(
		"*downloaddir:* downloaddir has been successfully changed to"+downloadDir,
		ud.Message.Chat.ID, false,
	)
}

// add takes an URL to a .torrent file to add it to transmission
func add(ud tgbotapi.Update, tokens []string) {
	if len(tokens) == 0 {
		send("*add:* needs at least one URL", ud.Message.Chat.ID, false)
		return
	}

	// loop over the URL/s and add them
	for _, url := range tokens {
		cmd := transmission.NewAddCmdByURL(url)

		torrent, err := Client.ExecuteAddCommand(cmd)
		if err != nil {
			send("*add:* "+err.Error(), ud.Message.Chat.ID, false)
			continue
		}

		// check if torrent.Name is empty, then an error happened
		if torrent.Name == "" {
			send("*add:* error adding "+url, ud.Message.Chat.ID, false)
			continue
		}
		send(fmt.Sprintf("*Added:* <%d> %s", torrent.ID, torrent.Name), ud.Message.Chat.ID, false)
	}
}

// receiveTorrent gets an update that potentially has a .torrent file to add
func receiveTorrent(ud tgbotapi.Update) {
	if ud.Message.Document == nil {
		return // has no document
	}

	// get the file ID and make the config
	fconfig := tgbotapi.FileConfig{
		FileID: ud.Message.Document.FileID,
	}
	file, err := Bot.GetFile(fconfig)
	if err != nil {
		send("*receiver:* "+err.Error(), ud.Message.Chat.ID, false)
		return
	}

	// add by file URL
	add(ud, []string{file.Link(BotToken)})
}

// search takes a query and returns torrents with match
func search(ud tgbotapi.Update, tokens []string) {
	// make sure that we got a query
	if len(tokens) == 0 {
		send("*search:* needs an argument", ud.Message.Chat.ID, false)
		return
	}

	query := strings.Join(tokens, " ")
	// "(?i)" for case insensitivity
	regx, err := regexp.Compile("(?i)" + query)
	if err != nil {
		send("*search:* "+err.Error(), ud.Message.Chat.ID, false)
		return
	}

	torrents, err := Client.GetTorrents()
	if err != nil {
		send("*search:* "+err.Error(), ud.Message.Chat.ID, false)
		return
	}

	buf := new(bytes.Buffer)
	for i := range torrents {
		if regx.MatchString(torrents[i].Name) {
			buf.WriteString(fmt.Sprintf("<%d> %s\n", torrents[i].ID, torrents[i].Name))
		}
	}
	if buf.Len() == 0 {
		send("No matches!", ud.Message.Chat.ID, false)
		return
	}
	send(buf.String(), ud.Message.Chat.ID, false)
}

// latest takes n and returns the latest n torrents
func latest(ud tgbotapi.Update, tokens []string) {
	var (
		n   = 5 // default to 5
		err error
	)

	if len(tokens) > 0 {
		n, err = strconv.Atoi(tokens[0])
		if err != nil {
			send("*latest:* argument must be a number", ud.Message.Chat.ID, false)
			return
		}
	}

	torrents, err := Client.GetTorrents()
	if err != nil {
		send("*latest:* "+err.Error(), ud.Message.Chat.ID, false)
		return
	}

	// make sure that we stay in the boundaries
	if n <= 0 || n > len(torrents) {
		n = len(torrents)
	}

	// sort by age, and set reverse to true to get the latest first
	torrents.SortAge(true)

	buf := new(bytes.Buffer)
	for i := range torrents[:n] {
		buf.WriteString(fmt.Sprintf("<%d> %s\n", torrents[i].ID, torrents[i].Name))
	}
	if buf.Len() == 0 {
		send("*latest:* No torrents", ud.Message.Chat.ID, false)
		return
	}
	send(buf.String(), ud.Message.Chat.ID, false)
}

// info takes an id of a torrent and returns some info about it
func info(ud tgbotapi.Update, tokens []string) {
	if len(tokens) == 0 {
		send("*info:* needs a torrent ID number", ud.Message.Chat.ID, false)
		return
	}

	for _, id := range tokens {
		torrentID, err := strconv.Atoi(id)
		if err != nil {
			send(fmt.Sprintf("*info:* %s is not a number", id), ud.Message.Chat.ID, false)
			continue
		}

		// get the torrent
		torrent, err := Client.GetTorrent(torrentID)
		if err != nil {
			send(fmt.Sprintf("*info:* Can't find a torrent with an ID of %d", torrentID), ud.Message.Chat.ID, false)
			continue
		}

		// get the trackers using 'trackerRegex'
		var trackers string
		for _, tracker := range torrent.Trackers {
			sm := trackerRegex.FindSubmatch([]byte(tracker.Announce))
			if len(sm) > 1 {
				trackers += string(sm[1]) + " "
			}
		}

		limitsInfo := ""
		limitsOut, limitsErr := rpcCall("torrent_get", map[string]any{
			"ids": []int{torrentID},
			"fields": []string{
				"download_limit", "download_limited", "upload_limit", "upload_limited",
				"peer_limit", "seed_ratio_limit", "seed_ratio_mode",
				"seed_idle_limit", "seed_idle_mode", "honors_session_limits",
			},
		})
		if limitsErr == nil && len(limitsOut.Result.Torrents) > 0 {
			lt := limitsOut.Result.Torrents[0]
			down := "off"
			if lt.DownloadLimited {
				down = fmt.Sprintf("%d KB/s", lt.DownloadLimit)
			}
			up := "off"
			if lt.UploadLimited {
				up = fmt.Sprintf("%d KB/s", lt.UploadLimit)
			}
			seedRatio := "default"
			if lt.SeedRatioMode == 1 {
				seedRatio = fmt.Sprintf("%.2f", lt.SeedRatioLimit)
			}
			seedIdle := "default"
			if lt.SeedIdleMode == 1 {
				seedIdle = fmt.Sprintf("%d min", lt.SeedIdleLimit)
			}
			limitsInfo = fmt.Sprintf("\nLimits: DL `%s` | UP `%s` | peers `%d`\nSeed: ratio `%s` | idle `%s` | global `%t`",
				down, up, lt.PeerLimit, seedRatio, seedIdle, lt.HonorsSessionLimits)
		}

		// format the info
		torrentName := mdReplacer.Replace(torrent.Name) // escape markdown
		info := fmt.Sprintf("`<%d>` *%s*\n%s *%s* of *%s* (*%.1f%%*) ↓ *%s*  ↑ *%s* R: *%s*\nDL: *%s* UP: *%s*\nAdded: *%s*, ETA: *%s*\nTrackers: `%s`%s\nFiles: use `files %d`",
			torrent.ID, torrentName, torrent.TorrentStatus(), humanize.Bytes(torrent.Have()), humanize.Bytes(torrent.SizeWhenDone),
			torrent.PercentDone*100, humanize.Bytes(torrent.RateDownload), humanize.Bytes(torrent.RateUpload), torrent.Ratio(),
			humanize.Bytes(torrent.DownloadedEver), humanize.Bytes(torrent.UploadedEver), time.Unix(torrent.AddedDate, 0).Format(time.Stamp),
			torrent.ETA(), trackers, limitsInfo, torrent.ID)

		// send it
		msgID := send(info, ud.Message.Chat.ID, true)

		if NoLive {
			return
		}

		// this go-routine will make the info live for 'duration * interval'
		go func(torrentID, msgID int) {
			for i := 0; i < duration; i++ {
				time.Sleep(time.Second * interval)
				torrent, err = Client.GetTorrent(torrentID)
				if err != nil {
					continue // skip this iteration if there's an error retrieving the torrent's info
				}

				torrentName := mdReplacer.Replace(torrent.Name)
				info := fmt.Sprintf("`<%d>` *%s*\n%s *%s* of *%s* (*%.1f%%*) ↓ *%s*  ↑ *%s* R: *%s*\nDL: *%s* UP: *%s*\nAdded: *%s*, ETA: *%s*\nTrackers: `%s`%s\nFiles: use `files %d`",
					torrent.ID, torrentName, torrent.TorrentStatus(), humanize.Bytes(torrent.Have()), humanize.Bytes(torrent.SizeWhenDone),
					torrent.PercentDone*100, humanize.Bytes(torrent.RateDownload), humanize.Bytes(torrent.RateUpload), torrent.Ratio(),
					humanize.Bytes(torrent.DownloadedEver), humanize.Bytes(torrent.UploadedEver), time.Unix(torrent.AddedDate, 0).Format(time.Stamp),
					torrent.ETA(), trackers, limitsInfo, torrent.ID)

				// update the message
				editConf := tgbotapi.NewEditMessageText(ud.Message.Chat.ID, msgID, info)
				editConf.ParseMode = tgbotapi.ModeMarkdown
				Bot.Send(editConf)

			}
			// sleep one more time before the dashes
			time.Sleep(time.Second * interval)

			// at the end write dashes to indicate that we are done being live.
			torrentName := mdReplacer.Replace(torrent.Name)
			info := fmt.Sprintf("`<%d>` *%s*\n%s *%s* of *%s* (*%.1f%%*) ↓ *- B*  ↑ *- B* R: *%s*\nDL: *%s* UP: *%s*\nAdded: *%s*, ETA: *-*\nTrackers: `%s`%s\nFiles: use `files %d`",
				torrent.ID, torrentName, torrent.TorrentStatus(), humanize.Bytes(torrent.Have()), humanize.Bytes(torrent.SizeWhenDone),
				torrent.PercentDone*100, torrent.Ratio(), humanize.Bytes(torrent.DownloadedEver), humanize.Bytes(torrent.UploadedEver),
				time.Unix(torrent.AddedDate, 0).Format(time.Stamp), trackers, limitsInfo, torrent.ID)

			editConf := tgbotapi.NewEditMessageText(ud.Message.Chat.ID, msgID, info)
			editConf.ParseMode = tgbotapi.ModeMarkdown
			Bot.Send(editConf)
		}(torrentID, msgID)
	}
}

func files(ud tgbotapi.Update, tokens []string) {
	if len(tokens) != 1 {
		send("*files:* usage: files <id>", ud.Message.Chat.ID, false)
		return
	}
	torrentID, err := parseIntToken(tokens[0])
	if err != nil {
		send("*files:* "+err.Error(), ud.Message.Chat.ID, false)
		return
	}
	out, err := rpcCall("torrent_get", map[string]any{
		"ids":    []int{torrentID},
		"fields": []string{"id", "name", "files", "file_stats"},
	})
	if err != nil {
		send("*files:* "+err.Error(), ud.Message.Chat.ID, false)
		return
	}
	if len(out.Result.Torrents) == 0 {
		send(fmt.Sprintf("*files:* no torrent with id %d", torrentID), ud.Message.Chat.ID, false)
		return
	}
	t := out.Result.Torrents[0]
	buf := new(bytes.Buffer)
	buf.WriteString(fmt.Sprintf("`<%d>` *%s*\n", t.ID, mdReplacer.Replace(t.Name)))
	for idx, file := range t.Files {
		fileStat := rpcFileStat{}
		if idx < len(t.FileStats) {
			fileStat = t.FileStats[idx]
		}
		progress := 0.0
		if file.Length > 0 {
			progress = float64(fileStat.BytesCompleted) * 100 / float64(file.Length)
		}
		state := "skip"
		if fileStat.Wanted {
			state = "want"
		}
		buf.WriteString(fmt.Sprintf("[%d] %s | %s | %s | %s / %s (%.1f%%)\n",
			idx, state, priorityName(fileStat.Priority), file.Name,
			humanize.Bytes(fileStat.BytesCompleted), humanize.Bytes(file.Length), progress))
	}
	send(buf.String(), ud.Message.Chat.ID, true)
}

func want(ud tgbotapi.Update, tokens []string) {
	updateFileSelection(ud, "want", tokens, true)
}

func skip(ud tgbotapi.Update, tokens []string) {
	updateFileSelection(ud, "skip", tokens, false)
}

func updateFileSelection(ud tgbotapi.Update, command string, tokens []string, wanted bool) {
	if len(tokens) < 2 {
		send(fmt.Sprintf("*%s:* usage: %s <id> <file...>", command, command), ud.Message.Chat.ID, false)
		return
	}
	torrentID, err := parseIntToken(tokens[0])
	if err != nil {
		send(fmt.Sprintf("*%s:* %s", command, err.Error()), ud.Message.Chat.ID, false)
		return
	}
	fileIDs, err := parseFileSelection(tokens[1:])
	if err != nil {
		send(fmt.Sprintf("*%s:* %s", command, err.Error()), ud.Message.Chat.ID, false)
		return
	}
	out, err := rpcCall("torrent_get", map[string]any{
		"ids":    []int{torrentID},
		"fields": []string{"id", "name", "files"},
	})
	if err != nil {
		send(fmt.Sprintf("*%s:* %s", command, err.Error()), ud.Message.Chat.ID, false)
		return
	}
	if len(out.Result.Torrents) == 0 {
		send(fmt.Sprintf("*%s:* no torrent with id %d", command, torrentID), ud.Message.Chat.ID, false)
		return
	}
	t := out.Result.Torrents[0]
	if err := validateFileBounds(fileIDs, len(t.Files)); err != nil {
		send(fmt.Sprintf("*%s:* %s", command, err.Error()), ud.Message.Chat.ID, false)
		return
	}

	params := map[string]any{
		"ids": []int{torrentID},
	}
	if wanted {
		params["files_wanted"] = fileIDs
	} else {
		params["files_unwanted"] = fileIDs
	}
	if _, err := rpcCall("torrent_set", params); err != nil {
		send(fmt.Sprintf("*%s:* %s", command, err.Error()), ud.Message.Chat.ID, false)
		return
	}
	send(fmt.Sprintf("*%s:* updated %d file(s) in `%s`", command, len(fileIDs), mdReplacer.Replace(t.Name)), ud.Message.Chat.ID, true)
}

func fprio(ud tgbotapi.Update, tokens []string) {
	if len(tokens) < 3 {
		send("*fprio:* usage: fprio <id> <high|normal|low> <file...>", ud.Message.Chat.ID, false)
		return
	}
	torrentID, err := parseIntToken(tokens[0])
	if err != nil {
		send("*fprio:* "+err.Error(), ud.Message.Chat.ID, false)
		return
	}
	level := strings.ToLower(tokens[1])
	targetField := ""
	switch level {
	case "high":
		targetField = "priority_high"
	case "normal":
		targetField = "priority_normal"
	case "low":
		targetField = "priority_low"
	default:
		send("*fprio:* level must be high, normal, or low", ud.Message.Chat.ID, false)
		return
	}
	fileIDs, err := parseFileSelection(tokens[2:])
	if err != nil {
		send("*fprio:* "+err.Error(), ud.Message.Chat.ID, false)
		return
	}
	out, err := rpcCall("torrent_get", map[string]any{
		"ids":    []int{torrentID},
		"fields": []string{"id", "name", "files"},
	})
	if err != nil {
		send("*fprio:* "+err.Error(), ud.Message.Chat.ID, false)
		return
	}
	if len(out.Result.Torrents) == 0 {
		send(fmt.Sprintf("*fprio:* no torrent with id %d", torrentID), ud.Message.Chat.ID, false)
		return
	}
	t := out.Result.Torrents[0]
	if err := validateFileBounds(fileIDs, len(t.Files)); err != nil {
		send("*fprio:* "+err.Error(), ud.Message.Chat.ID, false)
		return
	}
	if _, err := rpcCall("torrent_set", map[string]any{
		"ids":       []int{torrentID},
		targetField: fileIDs,
	}); err != nil {
		send("*fprio:* "+err.Error(), ud.Message.Chat.ID, false)
		return
	}
	send(fmt.Sprintf("*fprio:* set %s priority for %d file(s) in `%s`", level, len(fileIDs), mdReplacer.Replace(t.Name)), ud.Message.Chat.ID, true)
}

func queueAction(ud tgbotapi.Update, command string, rpcMethod string, tokens []string) {
	ids, err := parseTorrentIDs(tokens)
	if err != nil {
		send(fmt.Sprintf("*%s:* %s", command, err.Error()), ud.Message.Chat.ID, false)
		return
	}

	if _, err := rpcCall(rpcMethod, map[string]any{"ids": ids}); err != nil {
		send(fmt.Sprintf("*%s:* %s", command, err.Error()), ud.Message.Chat.ID, false)
		return
	}

	out, err := rpcCall("torrent_get", map[string]any{
		"ids":    ids,
		"fields": []string{"id", "name", "queue_position"},
	})
	if err != nil {
		send(fmt.Sprintf("*%s:* done, but failed to fetch updated queue: %s", command, err.Error()), ud.Message.Chat.ID, false)
		return
	}

	if len(out.Result.Torrents) == 0 {
		send(fmt.Sprintf("*%s:* done", command), ud.Message.Chat.ID, false)
		return
	}

	buf := new(bytes.Buffer)
	buf.WriteString(fmt.Sprintf("*%s:* done\n", command))
	for _, t := range out.Result.Torrents {
		buf.WriteString(fmt.Sprintf("<%d> %s (queue: %d)\n", t.ID, t.Name, t.QueuePosition))
	}
	send(buf.String(), ud.Message.Chat.ID, true)
}

func updateSingleTorrent(ud tgbotapi.Update, command string, torrentID int, params map[string]any) {
	params["ids"] = []int{torrentID}
	if _, err := rpcCall("torrent_set", params); err != nil {
		send(fmt.Sprintf("*%s:* %s", command, err.Error()), ud.Message.Chat.ID, false)
		return
	}
	out, err := rpcCall("torrent_get", map[string]any{
		"ids":    []int{torrentID},
		"fields": []string{"id", "name"},
	})
	if err != nil || len(out.Result.Torrents) == 0 {
		send(fmt.Sprintf("*%s:* updated torrent %d", command, torrentID), ud.Message.Chat.ID, false)
		return
	}
	send(fmt.Sprintf("*%s:* updated `<%d> %s`", command, torrentID, mdReplacer.Replace(out.Result.Torrents[0].Name)), ud.Message.Chat.ID, true)
}

func tlimit(ud tgbotapi.Update, tokens []string) {
	if len(tokens) != 3 {
		send("*tlimit:* usage: tlimit <id> <down|up> <kbps>", ud.Message.Chat.ID, false)
		return
	}
	torrentID, err := parseIntToken(tokens[0])
	if err != nil {
		send("*tlimit:* "+err.Error(), ud.Message.Chat.ID, false)
		return
	}
	value, err := strconv.Atoi(tokens[2])
	if err != nil || value < 0 {
		send("*tlimit:* kbps must be a non-negative number", ud.Message.Chat.ID, false)
		return
	}
	dir := strings.ToLower(tokens[1])
	params := map[string]any{}
	switch dir {
	case "down":
		params["download_limit"] = value
		params["download_limited"] = true
	case "up":
		params["upload_limit"] = value
		params["upload_limited"] = true
	default:
		send("*tlimit:* direction must be down or up", ud.Message.Chat.ID, false)
		return
	}
	updateSingleTorrent(ud, "tlimit", torrentID, params)
}

func tpeers(ud tgbotapi.Update, tokens []string) {
	if len(tokens) != 2 {
		send("*tpeers:* usage: tpeers <id> <count>", ud.Message.Chat.ID, false)
		return
	}
	torrentID, err := parseIntToken(tokens[0])
	if err != nil {
		send("*tpeers:* "+err.Error(), ud.Message.Chat.ID, false)
		return
	}
	count, err := strconv.Atoi(tokens[1])
	if err != nil || count < 0 {
		send("*tpeers:* count must be a non-negative number", ud.Message.Chat.ID, false)
		return
	}
	updateSingleTorrent(ud, "tpeers", torrentID, map[string]any{"peer_limit": count})
}

func tratio(ud tgbotapi.Update, tokens []string) {
	if len(tokens) != 2 {
		send("*tratio:* usage: tratio <id> <limit>", ud.Message.Chat.ID, false)
		return
	}
	torrentID, err := parseIntToken(tokens[0])
	if err != nil {
		send("*tratio:* "+err.Error(), ud.Message.Chat.ID, false)
		return
	}
	limit, err := strconv.ParseFloat(tokens[1], 64)
	if err != nil || limit < 0 {
		send("*tratio:* limit must be a non-negative number", ud.Message.Chat.ID, false)
		return
	}
	updateSingleTorrent(ud, "tratio", torrentID, map[string]any{"seed_ratio_limit": limit, "seed_ratio_mode": 1})
}

func tidle(ud tgbotapi.Update, tokens []string) {
	if len(tokens) != 2 {
		send("*tidle:* usage: tidle <id> <minutes>", ud.Message.Chat.ID, false)
		return
	}
	torrentID, err := parseIntToken(tokens[0])
	if err != nil {
		send("*tidle:* "+err.Error(), ud.Message.Chat.ID, false)
		return
	}
	minutes, err := strconv.Atoi(tokens[1])
	if err != nil || minutes < 0 {
		send("*tidle:* minutes must be a non-negative number", ud.Message.Chat.ID, false)
		return
	}
	updateSingleTorrent(ud, "tidle", torrentID, map[string]any{"seed_idle_limit": minutes, "seed_idle_mode": 1})
}

func tglobal(ud tgbotapi.Update, tokens []string) {
	if len(tokens) != 2 {
		send("*tglobal:* usage: tglobal <id> <on|off>", ud.Message.Chat.ID, false)
		return
	}
	torrentID, err := parseIntToken(tokens[0])
	if err != nil {
		send("*tglobal:* "+err.Error(), ud.Message.Chat.ID, false)
		return
	}
	state := strings.ToLower(tokens[1])
	var enabled bool
	switch state {
	case "on":
		enabled = true
	case "off":
		enabled = false
	default:
		send("*tglobal:* value must be on or off", ud.Message.Chat.ID, false)
		return
	}
	updateSingleTorrent(ud, "tglobal", torrentID, map[string]any{"honors_session_limits": enabled})
}

func trackerlist(ud tgbotapi.Update, tokens []string) {
	if len(tokens) != 1 {
		send("*trackerlist:* usage: trackerlist <id>", ud.Message.Chat.ID, false)
		return
	}
	torrentID, err := parseIntToken(tokens[0])
	if err != nil {
		send("*trackerlist:* "+err.Error(), ud.Message.Chat.ID, false)
		return
	}
	out, err := rpcCall("torrent_get", map[string]any{
		"ids":    []int{torrentID},
		"fields": []string{"id", "name", "trackers", "tracker_list"},
	})
	if err != nil {
		send("*trackerlist:* "+err.Error(), ud.Message.Chat.ID, false)
		return
	}
	if len(out.Result.Torrents) == 0 {
		send(fmt.Sprintf("*trackerlist:* no torrent with id %d", torrentID), ud.Message.Chat.ID, false)
		return
	}
	t := out.Result.Torrents[0]
	buf := new(bytes.Buffer)
	buf.WriteString(fmt.Sprintf("`<%d>` *%s*\n", t.ID, mdReplacer.Replace(t.Name)))
	if len(t.Trackers) > 0 {
		for _, tr := range t.Trackers {
			buf.WriteString(fmt.Sprintf("tier %d #%d: %s\n", tr.Tier, tr.ID, tr.Announce))
		}
	} else if strings.TrimSpace(t.TrackerList) != "" {
		buf.WriteString(t.TrackerList)
	} else {
		buf.WriteString("No trackers")
	}
	send(buf.String(), ud.Message.Chat.ID, true)
}

func trackerset(ud tgbotapi.Update, tokens []string) {
	body := commandTail(ud.Message.Text)
	if body == "" {
		send("*trackerset:* usage: trackerset <id> <tracker_list>", ud.Message.Chat.ID, false)
		return
	}
	body = strings.TrimSpace(body)
	cut := strings.IndexAny(body, " \n\t")
	if cut < 0 {
		send("*trackerset:* missing tracker list content", ud.Message.Chat.ID, false)
		return
	}

	torrentID, err := parseIntToken(body[:cut])
	if err != nil {
		send("*trackerset:* "+err.Error(), ud.Message.Chat.ID, false)
		return
	}
	trackerList := strings.TrimSpace(body[cut+1:])
	if trackerList == "" {
		send("*trackerset:* missing tracker list content", ud.Message.Chat.ID, false)
		return
	}

	if _, err := rpcCall("torrent_set", map[string]any{
		"ids":          []int{torrentID},
		"tracker_list": trackerList,
	}); err != nil {
		send("*trackerset:* "+err.Error(), ud.Message.Chat.ID, false)
		return
	}

	send(fmt.Sprintf("*trackerset:* replaced trackers for torrent `%d`", torrentID), ud.Message.Chat.ID, true)
}

func paths(ud tgbotapi.Update, tokens []string) {
	if len(tokens) != 1 {
		send("*paths:* usage: paths <id>", ud.Message.Chat.ID, false)
		return
	}
	torrentID, err := parseIntToken(tokens[0])
	if err != nil {
		send("*paths:* "+err.Error(), ud.Message.Chat.ID, false)
		return
	}
	out, err := rpcCall("torrent_get", map[string]any{
		"ids":    []int{torrentID},
		"fields": []string{"id", "name", "download_dir", "files"},
	})
	if err != nil {
		send("*paths:* "+err.Error(), ud.Message.Chat.ID, false)
		return
	}
	if len(out.Result.Torrents) == 0 {
		send(fmt.Sprintf("*paths:* no torrent with id %d", torrentID), ud.Message.Chat.ID, false)
		return
	}
	t := out.Result.Torrents[0]
	buf := new(bytes.Buffer)
	buf.WriteString(fmt.Sprintf("`<%d>` *%s*\nDownload dir: `%s`\n", t.ID, mdReplacer.Replace(t.Name), t.DownloadDir))
	for idx, f := range t.Files {
		buf.WriteString(fmt.Sprintf("[%d] `%s`\n", idx, f.Name))
	}
	send(buf.String(), ud.Message.Chat.ID, true)
}

func move(ud tgbotapi.Update) {
	args := splitCommandArgs(commandTail(ud.Message.Text))
	if len(args) < 2 {
		send("*move:* usage: move <id> <path> (quote paths with spaces)", ud.Message.Chat.ID, false)
		return
	}
	torrentID, err := parseIntToken(args[0])
	if err != nil {
		send("*move:* "+err.Error(), ud.Message.Chat.ID, false)
		return
	}
	newPath := strings.Join(args[1:], " ")
	if _, err := rpcCall("torrent_set_location", map[string]any{
		"ids":      []int{torrentID},
		"location": newPath,
		"move":     true,
	}); err != nil {
		send("*move:* "+err.Error(), ud.Message.Chat.ID, false)
		return
	}
	out, err := rpcCall("torrent_get", map[string]any{
		"ids":    []int{torrentID},
		"fields": []string{"id", "name", "download_dir"},
	})
	if err != nil || len(out.Result.Torrents) == 0 {
		send(fmt.Sprintf("*move:* moved torrent `%d` to `%s`", torrentID, newPath), ud.Message.Chat.ID, true)
		return
	}
	t := out.Result.Torrents[0]
	send(fmt.Sprintf("*move:* `<%d> %s` now in `%s`", t.ID, mdReplacer.Replace(t.Name), t.DownloadDir), ud.Message.Chat.ID, true)
}

func rename(ud tgbotapi.Update) {
	args := splitCommandArgs(commandTail(ud.Message.Text))
	if len(args) < 3 {
		send("*rename:* usage: rename <id> <oldpath> <newname> (quote paths with spaces)", ud.Message.Chat.ID, false)
		return
	}
	torrentID, err := parseIntToken(args[0])
	if err != nil {
		send("*rename:* "+err.Error(), ud.Message.Chat.ID, false)
		return
	}
	oldPath := args[1]
	newName := strings.Join(args[2:], " ")
	if _, err := rpcCall("torrent_rename_path", map[string]any{
		"ids":  []int{torrentID},
		"path": oldPath,
		"name": newName,
	}); err != nil {
		send("*rename:* "+err.Error(), ud.Message.Chat.ID, false)
		return
	}
	out, err := rpcCall("torrent_get", map[string]any{
		"ids":    []int{torrentID},
		"fields": []string{"id", "name", "files"},
	})
	if err != nil || len(out.Result.Torrents) == 0 {
		send(fmt.Sprintf("*rename:* renamed `%s` -> `%s`", oldPath, newName), ud.Message.Chat.ID, true)
		return
	}
	t := out.Result.Torrents[0]
	send(fmt.Sprintf("*rename:* updated `<%d> %s` (`%s` -> `%s`)", t.ID, mdReplacer.Replace(t.Name), oldPath, newName), ud.Message.Chat.ID, true)
}

// stop takes id[s] of torrent[s] or 'all' to stop them
func stop(ud tgbotapi.Update, tokens []string) {
	// make sure that we got at least one argument
	if len(tokens) == 0 {
		send("*stop:* needs an argument", ud.Message.Chat.ID, false)
		return
	}

	// if the first argument is 'all' then stop all torrents
	if tokens[0] == "all" {
		if err := Client.StopAll(); err != nil {
			send("*stop:* error occurred while stopping some torrents", ud.Message.Chat.ID, false)
			return
		}
		send("Stopped all torrents", ud.Message.Chat.ID, false)
		return
	}

	for _, id := range tokens {
		num, err := strconv.Atoi(id)
		if err != nil {
			send(fmt.Sprintf("*stop:* %s is not a number", id), ud.Message.Chat.ID, false)
			continue
		}
		status, err := Client.StopTorrent(num)
		if err != nil {
			send("*stop:* "+err.Error(), ud.Message.Chat.ID, false)
			continue
		}

		torrent, err := Client.GetTorrent(num)
		if err != nil {
			send(fmt.Sprintf("[fail] *stop:* No torrent with an ID of %d", num), ud.Message.Chat.ID, false)
			return
		}
		send(fmt.Sprintf("[%s] *stop:* %s", status, torrent.Name), ud.Message.Chat.ID, false)
	}
}

// start takes id[s] of torrent[s] or 'all' to start them
func start(ud tgbotapi.Update, tokens []string) {
	// make sure that we got at least one argument
	if len(tokens) == 0 {
		send("*start:* needs an argument", ud.Message.Chat.ID, false)
		return
	}

	// if the first argument is 'all' then start all torrents
	if tokens[0] == "all" {
		if err := Client.StartAll(); err != nil {
			send("*start:* error occurred while starting some torrents", ud.Message.Chat.ID, false)
			return
		}
		send("Started all torrents", ud.Message.Chat.ID, false)
		return

	}

	for _, id := range tokens {
		num, err := strconv.Atoi(id)
		if err != nil {
			send(fmt.Sprintf("*start:* %s is not a number", id), ud.Message.Chat.ID, false)
			continue
		}
		status, err := Client.StartTorrent(num)
		if err != nil {
			send("*start:* "+err.Error(), ud.Message.Chat.ID, false)
			continue
		}

		torrent, err := Client.GetTorrent(num)
		if err != nil {
			send(fmt.Sprintf("[fail] *start:* No torrent with an ID of %d", num), ud.Message.Chat.ID, false)
			return
		}
		send(fmt.Sprintf("[%s] *start:* %s", status, torrent.Name), ud.Message.Chat.ID, false)
	}
}

// check takes id[s] of torrent[s] or 'all' to verify them
func check(ud tgbotapi.Update, tokens []string) {
	// make sure that we got at least one argument
	if len(tokens) == 0 {
		send("*check:* needs an argument", ud.Message.Chat.ID, false)
		return
	}

	// if the first argument is 'all' then start all torrents
	if tokens[0] == "all" {
		if err := Client.VerifyAll(); err != nil {
			send("*check:* error occurred while verifying some torrents", ud.Message.Chat.ID, false)
			return
		}
		send("Verifying all torrents", ud.Message.Chat.ID, false)
		return

	}

	for _, id := range tokens {
		num, err := strconv.Atoi(id)
		if err != nil {
			send(fmt.Sprintf("*check:* %s is not a number", id), ud.Message.Chat.ID, false)
			continue
		}
		status, err := Client.VerifyTorrent(num)
		if err != nil {
			send("*check:* "+err.Error(), ud.Message.Chat.ID, false)
			continue
		}

		torrent, err := Client.GetTorrent(num)
		if err != nil {
			send(fmt.Sprintf("[fail] *check:* No torrent with an ID of %d", num), ud.Message.Chat.ID, false)
			return
		}
		send(fmt.Sprintf("[%s] *check:* %s", status, torrent.Name), ud.Message.Chat.ID, false)
	}

}

// stats echo back transmission stats
func stats(ud tgbotapi.Update) {
	stats, err := Client.GetStats()
	if err != nil {
		send("*stats:* "+err.Error(), ud.Message.Chat.ID, false)
		return
	}

	msg := fmt.Sprintf(
		`
		Total: *%d*
		Active: *%d*
		Paused: *%d*

		_Current Stats_
		Downloaded: *%s*
		Uploaded: *%s*
		Running time: *%s*

		_Accumulative Stats_
		Sessions: *%d*
		Downloaded: *%s*
		Uploaded: *%s*
		Total Running time: *%s*
		`,

		stats.TorrentCount,
		stats.ActiveTorrentCount,
		stats.PausedTorrentCount,
		humanize.Bytes(stats.CurrentStats.DownloadedBytes),
		humanize.Bytes(stats.CurrentStats.UploadedBytes),
		stats.CurrentActiveTime(),
		stats.CumulativeStats.SessionCount,
		humanize.Bytes(stats.CumulativeStats.DownloadedBytes),
		humanize.Bytes(stats.CumulativeStats.UploadedBytes),
		stats.CumulativeActiveTime(),
	)

	send(msg, ud.Message.Chat.ID, true)
}

// downlimit sets the global downlimit to a provided value in kilobytes
func downlimit(ud tgbotapi.Update, tokens []string) {
	speedLimit(ud, tokens, transmission.DownloadLimitType)
}

// uplimit sets the global uplimit to a provided value in kilobytes
func uplimit(ud tgbotapi.Update, tokens []string) {
	speedLimit(ud, tokens, transmission.UploadLimitType)
}

// speedLimit sets either a donwload or upload limit
func speedLimit(ud tgbotapi.Update, tokens []string, limitType transmission.SpeedLimitType) {
	if len(tokens) < 1 {
		send("Please, specify the limit", ud.Message.Chat.ID, false)
		return
	}

	limit, err := strconv.ParseUint(tokens[0], 10, 32)
	if err != nil {
		send("Please, specify the limit as number of kilobytes", ud.Message.Chat.ID, false)
		return
	}

	speedLimitCmd := transmission.NewSpeedLimitCommand(limitType, uint(limit))
	if speedLimitCmd == nil {
		send(fmt.Sprintf("*%s:* internal error", limitType), ud.Message.Chat.ID, false)
		return
	}

	out, err := Client.ExecuteCommand(speedLimitCmd)
	if err != nil {
		send(fmt.Sprintf("*%s:* %v", limitType, err.Error()), ud.Message.Chat.ID, false)
		return
	}
	if out.Result != "success" {
		send(fmt.Sprintf("*%s:* %v", limitType, out.Result), ud.Message.Chat.ID, false)
		return
	}

	send(
		fmt.Sprintf("*%s:* limit has been successfully changed to %d KB/s", limitType, limit),
		ud.Message.Chat.ID, false,
	)
}

// speed will echo back the current download and upload speeds
func speed(ud tgbotapi.Update) {
	stats, err := Client.GetStats()
	if err != nil {
		send("*speed:* "+err.Error(), ud.Message.Chat.ID, false)
		return
	}

	msg := fmt.Sprintf("↓ %s  ↑ %s", humanize.Bytes(stats.DownloadSpeed), humanize.Bytes(stats.UploadSpeed))

	msgID := send(msg, ud.Message.Chat.ID, false)

	if NoLive {
		return
	}

	for i := 0; i < duration; i++ {
		time.Sleep(time.Second * interval)
		stats, err = Client.GetStats()
		if err != nil {
			continue
		}

		msg = fmt.Sprintf("↓ %s  ↑ %s", humanize.Bytes(stats.DownloadSpeed), humanize.Bytes(stats.UploadSpeed))

		editConf := tgbotapi.NewEditMessageText(ud.Message.Chat.ID, msgID, msg)
		Bot.Send(editConf)
		time.Sleep(time.Second * interval)
	}
	// sleep one more time before switching to dashes
	time.Sleep(time.Second * interval)

	// show dashes to indicate that we are done updating.
	editConf := tgbotapi.NewEditMessageText(ud.Message.Chat.ID, msgID, "↓ - B  ↑ - B")
	Bot.Send(editConf)
}

// count returns current torrents count per status
func count(ud tgbotapi.Update) {
	torrents, err := Client.GetTorrents()
	if err != nil {
		send("*count:* "+err.Error(), ud.Message.Chat.ID, false)
		return
	}

	var downloading, seeding, stopped, checking, downloadingQ, seedingQ, checkingQ int

	for i := range torrents {
		switch torrents[i].Status {
		case transmission.StatusDownloading:
			downloading++
		case transmission.StatusSeeding:
			seeding++
		case transmission.StatusStopped:
			stopped++
		case transmission.StatusChecking:
			checking++
		case transmission.StatusDownloadPending:
			downloadingQ++
		case transmission.StatusSeedPending:
			seedingQ++
		case transmission.StatusCheckPending:
			checkingQ++
		}
	}

	msg := fmt.Sprintf("Downloading: %d\nSeeding: %d\nPaused: %d\nVerifying: %d\n\n- Waiting to -\nDownload: %d\nSeed: %d\nVerify: %d\n\nTotal: %d",
		downloading, seeding, stopped, checking, downloadingQ, seedingQ, checkingQ, len(torrents))

	send(msg, ud.Message.Chat.ID, false)

}

// del takes an id or more, and delete the corresponding torrent/s
func del(ud tgbotapi.Update, tokens []string) {
	// make sure that we got an argument
	if len(tokens) == 0 {
		send("*del:* needs an ID", ud.Message.Chat.ID, false)
		return
	}

	// loop over tokens to read each potential id
	for _, id := range tokens {
		num, err := strconv.Atoi(id)
		if err != nil {
			send(fmt.Sprintf("*del:* %s is not an ID", id), ud.Message.Chat.ID, false)
			return
		}

		name, err := Client.DeleteTorrent(num, false)
		if err != nil {
			send("*del:* "+err.Error(), ud.Message.Chat.ID, false)
			return
		}

		send("*Deleted:* "+name, ud.Message.Chat.ID, false)
	}
}

// deldata takes an id or more, and delete the corresponding torrent/s with their data
func deldata(ud tgbotapi.Update, tokens []string) {
	// make sure that we got an argument
	if len(tokens) == 0 {
		send("*deldata:* needs an ID", ud.Message.Chat.ID, false)
		return
	}
	// loop over tokens to read each potential id
	for _, id := range tokens {
		num, err := strconv.Atoi(id)
		if err != nil {
			send(fmt.Sprintf("*deldata:* %s is not an ID", id), ud.Message.Chat.ID, false)
			return
		}

		name, err := Client.DeleteTorrent(num, true)
		if err != nil {
			send("*deldata:* "+err.Error(), ud.Message.Chat.ID, false)
			return
		}

		send("Deleted with data: "+name, ud.Message.Chat.ID, false)
	}
}

// getVersion sends transmission version + transmission-telegram version
func getVersion(ud tgbotapi.Update) {
	send(fmt.Sprintf("Transmission *%s*\nTransmission-telegram *%s*", Client.Version(), VERSION), ud.Message.Chat.ID, true)
}

// send takes a chat id and a message to send, returns the message id of the send message
func send(text string, chatID int64, markdown bool) int {
	// set typing action
	action := tgbotapi.NewChatAction(chatID, tgbotapi.ChatTyping)
	Bot.Send(action)

	// check the rune count, telegram is limited to 4096 chars per message;
	// so if our message is > 4096, split it in chunks the send them.
	msgRuneCount := utf8.RuneCountInString(text)
LenCheck:
	stop := 4095
	if msgRuneCount > 4096 {
		for text[stop] != 10 { // '\n'
			stop--
		}
		msg := tgbotapi.NewMessage(chatID, text[:stop])
		msg.DisableWebPagePreview = true
		if markdown {
			msg.ParseMode = tgbotapi.ModeMarkdown
		}

		// send current chunk
		if _, err := Bot.Send(msg); err != nil {
			logger.Printf("[ERROR] Send: %s", err)
		}
		// move to the next chunk
		text = text[stop:]
		msgRuneCount = utf8.RuneCountInString(text)
		goto LenCheck
	}

	// if msgRuneCount < 4096, send it normally
	msg := tgbotapi.NewMessage(chatID, text)
	msg.DisableWebPagePreview = true
	if markdown {
		msg.ParseMode = tgbotapi.ModeMarkdown
	}

	resp, err := Bot.Send(msg)
	if err != nil {
		logger.Printf("[ERROR] Send: %s", err)
	}

	return resp.MessageID
}
