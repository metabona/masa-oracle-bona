package twitter

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	_ "github.com/lib/pq"

	twitterscraper "github.com/masa-finance/masa-twitter-scraper"
	"github.com/sirupsen/logrus"

	"github.com/masa-finance/masa-oracle/pkg/config"
	"github.com/masa-finance/masa-oracle/pkg/llmbridge"

	"io"
	"os"
	"strconv"
	"sync"
)

type TweetResult struct {
	Tweet *twitterscraper.Tweet
	Error error
}

// Biến toàn cục để lưu chỉ số hiện tại của cookieFiles
var cookieFilesIndex int
var cookieFiles []string
var cookieFilesOnce sync.Once

var cacheFilePath string

func init() {
	// Lấy đường dẫn thư mục home của người dùng
	homeDir, err := os.UserHomeDir()
	if err != nil {
		panic(err)
	}

	// Gán đường dẫn của file cache
	cacheFilePath = filepath.Join(homeDir, "file_cache.txt")
}

// initCookieFiles khởi tạo danh sách các file cookie
func initCookieFiles() {
	appConfig := config.GetInstance()

	// Kiểm tra xem thư mục MasaDir có tồn tại không
	files, err := os.ReadDir(appConfig.MasaDir)
	if err != nil {
		panic(err)
	}

	cookieFiles = []string{}
	for _, f := range files {
		if !f.IsDir() && strings.Contains(f.Name(), "twitter_cookie") {
			cookieFiles = append(cookieFiles, filepath.Join(appConfig.MasaDir, f.Name()))
		}
	}
	logrus.Warning(cookieFiles)

	// Đọc chỉ số từ cache nếu có
	if _, err := os.Stat(cacheFilePath); err == nil {
		// File cache tồn tại, đọc nội dung
		cacheIndex, err := os.ReadFile(cacheFilePath)
		if err == nil {
			cookieFilesIndex, _ = strconv.Atoi(string(cacheIndex))
		}
	} else {
		// File cache không tồn tại, khởi tạo chỉ số bắt đầu từ 0
		cookieFilesIndex = 0

		// Tạo file cache
		file, err := os.Create(cacheFilePath)
		if err != nil {
			panic(err)
		}
		file.Close() // Đóng file sau khi tạo
	}
}

// getNextCookieFile trả về file cookie tiếp theo trong danh sách
func getNextCookieFile() string {
	cookieFilesOnce.Do(initCookieFiles) // Khởi tạo danh sách file cookie một lần

	// Lấy file cookie theo thứ tự
	nextFile := cookieFiles[cookieFilesIndex]
	cookieFilesIndex = (cookieFilesIndex + 1) % len(cookieFiles) // Tăng chỉ số và quay lại đầu nếu vượt quá

	// Cập nhật chỉ số vào cache
	file, err := os.OpenFile(cacheFilePath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		panic(err)
	}
	defer file.Close()

	_, err = io.WriteString(file, strconv.Itoa(cookieFilesIndex))
	if err != nil {
		panic(err)
	}

	return nextFile
}

// auth initializes and returns a new Twitter scraper instance. It attempts to load cookies from a file to reuse an existing session.
// If no valid session is found, it performs a login with credentials specified in the application's configuration.
// On successful login, it saves the session cookies for future use. If the login fails, it returns nil.
func auth() *twitterscraper.Scraper {
	scraper := twitterscraper.New()
	appConfig := config.GetInstance()
	// cookieFilePath := filepath.Join(appConfig.MasaDir, "twitter_cookies.json")
	cookieFilePath := getNextCookieFile()

	logrus.Warning("cookieFilePath=")
	logrus.Warning(cookieFilePath)

	if err := LoadCookies(scraper, cookieFilePath); err == nil {
		logrus.Debug("Cookies loaded successfully.")
		if IsLoggedIn(scraper) {
			logrus.Debug("Already logged in via cookies.")
			return scraper
		}
	}

	username := appConfig.TwitterUsername
	password := appConfig.TwitterPassword
	twoFACode := appConfig.Twitter2FaCode

	var err error
	if twoFACode != "" {
		err = Login(scraper, username, password, twoFACode)
	} else {
		err = Login(scraper, username, password)
	}

	if err != nil {
		logrus.WithError(err).Warning("[-] Login failed")
		return nil
	}

	if err = SaveCookies(scraper, cookieFilePath); err != nil {
		logrus.WithError(err).Error("[-] Failed to save cookies")
	}

	logrus.WithFields(logrus.Fields{
		"auth":     true,
		"username": username,
	}).Debug("Login successful")

	return scraper
}

// ScrapeTweetsForSentiment is a function that scrapes tweets based on a given query, analyzes their sentiment using a specified model, and returns the sentiment analysis results.
// Parameters:
//   - query: The search query string to find matching tweets.
//   - count: The maximum number of tweets to retrieve and analyze.
//   - model: The model to use for sentiment analysis.
//
// Returns:
//   - A string representing the sentiment analysis prompt.
//   - A string representing the sentiment analysis result.
//   - An error if the scraping or sentiment analysis process encounters any issues.
func ScrapeTweetsForSentiment(query string, count int, model string) (string, string, error) {
	scraper := auth()
	var tweets []*TweetResult

	if scraper == nil {
		return "", "", fmt.Errorf("there was an error authenticating with your Twitter credentials")
	}

	// Set search mode
	scraper.SetSearchMode(twitterscraper.SearchLatest)

	// Perform the search with the specified query and count
	for tweetResult := range scraper.SearchTweets(context.Background(), query, count) {
		var tweet TweetResult
		if tweetResult.Error != nil {
			tweet = TweetResult{
				Tweet: nil,
				Error: tweetResult.Error,
			}
		} else {
			tweet = TweetResult{
				Tweet: &tweetResult.Tweet,
				Error: nil,
			}
		}
		tweets = append(tweets, &tweet)
	}
	sentimentPrompt := "Please perform a sentiment analysis on the following tweets, using an unbiased approach. Sentiment analysis involves identifying and categorizing opinions expressed in text, particularly to determine whether the writer's attitude towards a particular topic, product, etc., is positive, negative, or neutral. After analyzing, please provide a summary of the overall sentiment expressed in these tweets, including the proportion of positive, negative, and neutral sentiments if applicable."

	twitterScraperTweets := make([]*twitterscraper.TweetResult, len(tweets))
	for i, tweet := range tweets {
		twitterScraperTweets[i] = &twitterscraper.TweetResult{
			Tweet: *tweet.Tweet,
			Error: tweet.Error,
		}
	}
	prompt, sentiment, err := llmbridge.AnalyzeSentimentTweets(twitterScraperTweets, model, sentimentPrompt)
	if err != nil {
		return "", "", err
	}
	return prompt, sentiment, tweets[0].Error
}

// ScrapeTweetsByQuery performs a search on Twitter for tweets matching the specified query.
// It fetches up to the specified count of tweets and returns a slice of Tweet pointers.
// Parameters:
//   - query: The search query string to find matching tweets.
//   - count: The maximum number of tweets to retrieve.
//
// Returns:
//   - A slice of pointers to twitterscraper.Tweet objects that match the search query.
//   - An error if the scraping process encounters any issues.
func ScrapeTweetsByQuery(query string, count int) ([]*TweetResult, error) {
	scraper := auth()
	var tweets []*TweetResult
	var lastError error

	if scraper == nil {
		return nil, fmt.Errorf("there was an error authenticating with your Twitter credentials")
	}

	// Set search mode
	scraper.SetSearchMode(twitterscraper.SearchLatest)

	// Perform the search with the specified query and count
	for tweetResult := range scraper.SearchTweets(context.Background(), query, count) {
		if tweetResult.Error != nil {
			lastError = tweetResult.Error
			logrus.Warnf("[+] Error encountered while scraping tweet: %v", tweetResult.Error)
			if strings.Contains(tweetResult.Error.Error(), "Rate limit exceeded") {
				return nil, fmt.Errorf("Twitter API rate limit exceeded (429 error)")
			}
			continue
		}
		tweets = append(tweets, &TweetResult{Tweet: &tweetResult.Tweet, Error: nil})
	}

	if len(tweets) == 0 && lastError != nil {
		return nil, lastError
	}

	return tweets, nil
}

// ScrapeTweetsByTrends scrapes the current trending topics on Twitter.
// It returns a slice of strings representing the trending topics.
// If an error occurs during the scraping process, it returns an error.
func ScrapeTweetsByTrends() ([]*TweetResult, error) {
	scraper := auth()
	var trendResults []*TweetResult

	if scraper == nil {
		return nil, fmt.Errorf("there was an error authenticating with your Twitter credentials")
	}

	// Set search mode
	scraper.SetSearchMode(twitterscraper.SearchLatest)

	trends, err := scraper.GetTrends()
	if err != nil {
		return nil, err
	}

	for _, trend := range trends {
		trendResult := &TweetResult{
			Tweet: &twitterscraper.Tweet{Text: trend},
			Error: nil,
		}
		trendResults = append(trendResults, trendResult)
	}

	return trendResults, trendResults[0].Error
}

// ScrapeTweetsProfile scrapes the profile and tweets of a specific Twitter user.
// It takes the username as a parameter and returns the scraped profile information and an error if any.
func ScrapeTweetsProfile(username string) (twitterscraper.Profile, error) {
	scraper := auth()

	if scraper == nil {
		return twitterscraper.Profile{}, fmt.Errorf("there was an error authenticating with your Twitter credentials")
	}

	// Set search mode
	scraper.SetSearchMode(twitterscraper.SearchLatest)

	profile, err := scraper.GetProfile(username)
	if err != nil {
		return twitterscraper.Profile{}, err
	}

	return profile, nil
}
