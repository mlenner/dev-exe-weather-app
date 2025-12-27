package srv

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"path/filepath"
	"runtime"
	"time"

	"srv.exe.dev/db"
)

type Server struct {
	DB           *sql.DB
	Hostname     string
	TemplatesDir string
	StaticDir    string
}

// Brooklyn, NY coordinates
const (
	brooklynLat = 40.6782
	brooklynLon = -73.9442
)

// Weather data from Open-Meteo API
type WeatherData struct {
	Temperature     float64
	FeelsLike       float64
	Humidity        int
	WindSpeed       float64
	WindDirection   int
	WeatherCode     int
	IsDay           bool
	Precipitation   float64
	CloudCover      int
	LastUpdated     string
	Condition       string
	ConditionEmoji  string
}

// HourlyForecast represents one hour of forecast data
type HourlyForecast struct {
	Time           string
	Hour           string
	Temperature    float64
	WeatherCode    int
	ConditionEmoji string
	PrecipProb     int
	IsDay          bool
}

type pageData struct {
	Hostname string
	Now      string
	Weather  *WeatherData
	Hourly   []HourlyForecast
	Error    string
}

// Open-Meteo API response structure
type openMeteoResponse struct {
	Current struct {
		Time              string  `json:"time"`
		Temperature2m     float64 `json:"temperature_2m"`
		ApparentTemp      float64 `json:"apparent_temperature"`
		RelativeHumidity  int     `json:"relative_humidity_2m"`
		WindSpeed10m      float64 `json:"wind_speed_10m"`
		WindDirection10m  int     `json:"wind_direction_10m"`
		WeatherCode       int     `json:"weather_code"`
		IsDay             int     `json:"is_day"`
		Precipitation     float64 `json:"precipitation"`
		CloudCover        int     `json:"cloud_cover"`
	} `json:"current"`
	Hourly struct {
		Time            []string  `json:"time"`
		Temperature2m   []float64 `json:"temperature_2m"`
		WeatherCode     []int     `json:"weather_code"`
		PrecipProb      []int     `json:"precipitation_probability"`
		IsDay           []int     `json:"is_day"`
	} `json:"hourly"`
}

func New(dbPath, hostname string) (*Server, error) {
	_, thisFile, _, _ := runtime.Caller(0)
	baseDir := filepath.Dir(thisFile)
	srv := &Server{
		Hostname:     hostname,
		TemplatesDir: filepath.Join(baseDir, "templates"),
		StaticDir:    filepath.Join(baseDir, "static"),
	}
	if err := srv.setUpDatabase(dbPath); err != nil {
		return nil, err
	}
	return srv, nil
}

func (s *Server) fetchWeather() (*WeatherData, []HourlyForecast, error) {
	url := fmt.Sprintf(
		"https://api.open-meteo.com/v1/forecast?latitude=%.4f&longitude=%.4f&current=temperature_2m,relative_humidity_2m,apparent_temperature,precipitation,weather_code,cloud_cover,wind_speed_10m,wind_direction_10m,is_day&hourly=temperature_2m,weather_code,precipitation_probability,is_day&temperature_unit=fahrenheit&wind_speed_unit=mph&precipitation_unit=inch&timezone=America%%2FNew_York&forecast_hours=24",
		brooklynLat, brooklynLon,
	)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, nil, fmt.Errorf("fetch weather: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("weather API returned status %d", resp.StatusCode)
	}

	var data openMeteoResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, nil, fmt.Errorf("decode weather: %w", err)
	}

	condition, emoji := weatherCodeToCondition(data.Current.WeatherCode, data.Current.IsDay == 1)

	weather := &WeatherData{
		Temperature:    data.Current.Temperature2m,
		FeelsLike:      data.Current.ApparentTemp,
		Humidity:       data.Current.RelativeHumidity,
		WindSpeed:      data.Current.WindSpeed10m,
		WindDirection:  data.Current.WindDirection10m,
		WeatherCode:    data.Current.WeatherCode,
		IsDay:          data.Current.IsDay == 1,
		Precipitation:  data.Current.Precipitation,
		CloudCover:     data.Current.CloudCover,
		LastUpdated:    data.Current.Time,
		Condition:      condition,
		ConditionEmoji: emoji,
	}

	// Build hourly forecast
	hourly := make([]HourlyForecast, 0, len(data.Hourly.Time))
	for i, timeStr := range data.Hourly.Time {
		if i >= len(data.Hourly.Temperature2m) || i >= len(data.Hourly.WeatherCode) {
			break
		}
		isDay := false
		if i < len(data.Hourly.IsDay) {
			isDay = data.Hourly.IsDay[i] == 1
		}
		_, hourEmoji := weatherCodeToCondition(data.Hourly.WeatherCode[i], isDay)
		
		// Parse time to get hour display
		hourDisplay := timeStr
		if t, err := time.Parse("2006-01-02T15:04", timeStr); err == nil {
			hourDisplay = t.Format("3 PM")
		}
		
		precipProb := 0
		if i < len(data.Hourly.PrecipProb) {
			precipProb = data.Hourly.PrecipProb[i]
		}
		
		hourly = append(hourly, HourlyForecast{
			Time:           timeStr,
			Hour:           hourDisplay,
			Temperature:    data.Hourly.Temperature2m[i],
			WeatherCode:    data.Hourly.WeatherCode[i],
			ConditionEmoji: hourEmoji,
			PrecipProb:     precipProb,
			IsDay:          isDay,
		})
	}

	return weather, hourly, nil
}

func weatherCodeToCondition(code int, isDay bool) (string, string) {
	switch code {
	case 0:
		if isDay {
			return "Clear sky", "☀️"
		}
		return "Clear sky", "🌙"
	case 1:
		if isDay {
			return "Mainly clear", "🌤️"
		}
		return "Mainly clear", "🌙"
	case 2:
		return "Partly cloudy", "⛅"
	case 3:
		return "Overcast", "☁️"
	case 45, 48:
		return "Foggy", "🌫️"
	case 51, 53, 55:
		return "Drizzle", "🌧️"
	case 56, 57:
		return "Freezing drizzle", "🌧️❄️"
	case 61, 63, 65:
		return "Rain", "🌧️"
	case 66, 67:
		return "Freezing rain", "🌧️❄️"
	case 71, 73, 75:
		return "Snow", "🌨️"
	case 77:
		return "Snow grains", "🌨️"
	case 80, 81, 82:
		return "Rain showers", "🌦️"
	case 85, 86:
		return "Snow showers", "🌨️"
	case 95:
		return "Thunderstorm", "⛈️"
	case 96, 99:
		return "Thunderstorm with hail", "⛈️"
	default:
		return "Unknown", "❓"
	}
}

func windDirectionToCompass(degrees int) string {
	directions := []string{"N", "NNE", "NE", "ENE", "E", "ESE", "SE", "SSE", "S", "SSW", "SW", "WSW", "W", "WNW", "NW", "NNW"}
	index := int(float64(degrees)/22.5+0.5) % 16
	return directions[index]
}

func (s *Server) HandleRoot(w http.ResponseWriter, r *http.Request) {
	now := time.Now()

	data := pageData{
		Hostname: s.Hostname,
		Now:      now.Format(time.RFC3339),
	}

	weather, hourly, err := s.fetchWeather()
	if err != nil {
		slog.Error("fetch weather", "error", err)
		data.Error = "Unable to fetch weather data. Please try again later."
	} else {
		data.Weather = weather
		data.Hourly = hourly
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.renderTemplate(w, "weather.html", data); err != nil {
		slog.Warn("render template", "url", r.URL.Path, "error", err)
	}
}

func (s *Server) HandleAPI(w http.ResponseWriter, r *http.Request) {
	weather, hourly, err := s.fetchWeather()
	if err != nil {
		slog.Error("fetch weather", "error", err)
		http.Error(w, "Unable to fetch weather", http.StatusServiceUnavailable)
		return
	}

	response := struct {
		Current *WeatherData     `json:"current"`
		Hourly  []HourlyForecast `json:"hourly"`
	}{
		Current: weather,
		Hourly:  hourly,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (s *Server) renderTemplate(w http.ResponseWriter, name string, data any) error {
	path := filepath.Join(s.TemplatesDir, name)
	funcs := template.FuncMap{
		"windDir": windDirectionToCompass,
	}
	tmpl, err := template.New(name).Funcs(funcs).ParseFiles(path)
	if err != nil {
		return fmt.Errorf("parse template %q: %w", name, err)
	}
	if err := tmpl.Execute(w, data); err != nil {
		return fmt.Errorf("execute template %q: %w", name, err)
	}
	return nil
}

// SetupDatabase initializes the database connection and runs migrations
func (s *Server) setUpDatabase(dbPath string) error {
	wdb, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("failed to open db: %w", err)
	}
	s.DB = wdb
	if err := db.RunMigrations(wdb); err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}
	return nil
}

// Serve starts the HTTP server with the configured routes
func (s *Server) Serve(addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.HandleRoot)
	mux.HandleFunc("GET /api/weather", s.HandleAPI)
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir(s.StaticDir))))
	slog.Info("starting server", "addr", addr)
	return http.ListenAndServe(addr, mux)
}
