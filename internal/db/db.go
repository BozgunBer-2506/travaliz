package db

import (
	"database/sql"
	"fmt"
	"math/rand"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type DB struct {
	conn *sql.DB
}

type Booking struct {
	ID         int64
	Ref        string
	Type       string
	FirstName  string
	LastName   string
	Email      string
	Phone      string
	FromCode   string
	ToCode     string
	Airline    string
	DepartTime string
	ArriveTime string
	Duration   string
	Stops      int
	HotelName  string
	Checkin    string
	Checkout   string
	Price      float64
	Currency   string
	CardLast4  string
	CreatedAt  time.Time
}

func Open(path string) (*DB, error) {
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if err := migrate(conn); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &DB{conn: conn}, nil
}

func migrate(conn *sql.DB) error {
	_, err := conn.Exec(`
CREATE TABLE IF NOT EXISTS bookings (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  ref         TEXT    UNIQUE NOT NULL,
  type        TEXT    NOT NULL,
  first_name  TEXT    NOT NULL,
  last_name   TEXT    NOT NULL,
  email       TEXT    NOT NULL,
  phone       TEXT    NOT NULL,
  from_code   TEXT,
  to_code     TEXT,
  airline     TEXT,
  depart_time TEXT,
  arrive_time TEXT,
  duration    TEXT,
  stops       INTEGER DEFAULT 0,
  hotel_name  TEXT,
  checkin     TEXT,
  checkout    TEXT,
  price       REAL    NOT NULL,
  currency    TEXT    NOT NULL DEFAULT 'USD',
  card_last4  TEXT    NOT NULL,
  created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
)`)
	return err
}

func (d *DB) CreateBooking(b *Booking) (string, error) {
	b.Ref = generateRef()
	_, err := d.conn.Exec(`
INSERT INTO bookings
  (ref, type, first_name, last_name, email, phone,
   from_code, to_code, airline, depart_time, arrive_time, duration, stops,
   hotel_name, checkin, checkout, price, currency, card_last4)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		b.Ref, b.Type, b.FirstName, b.LastName, b.Email, b.Phone,
		b.FromCode, b.ToCode, b.Airline, b.DepartTime, b.ArriveTime, b.Duration, b.Stops,
		b.HotelName, b.Checkin, b.Checkout, b.Price, b.Currency, b.CardLast4,
	)
	if err != nil {
		return "", fmt.Errorf("insert booking: %w", err)
	}
	return b.Ref, nil
}

func (d *DB) GetBookingsByEmail(email string) ([]*Booking, error) {
	rows, err := d.conn.Query(`SELECT id,ref,type,first_name,last_name,email,phone,
	  from_code,to_code,airline,depart_time,arrive_time,duration,stops,
	  hotel_name,checkin,checkout,price,currency,card_last4,created_at
	FROM bookings WHERE email=? ORDER BY created_at DESC`, email)
	if err != nil {
		return nil, fmt.Errorf("query bookings: %w", err)
	}
	defer rows.Close()
	var bookings []*Booking
	for rows.Next() {
		b := &Booking{}
		if err := rows.Scan(
			&b.ID, &b.Ref, &b.Type, &b.FirstName, &b.LastName, &b.Email, &b.Phone,
			&b.FromCode, &b.ToCode, &b.Airline, &b.DepartTime, &b.ArriveTime, &b.Duration, &b.Stops,
			&b.HotelName, &b.Checkin, &b.Checkout, &b.Price, &b.Currency, &b.CardLast4, &b.CreatedAt,
		); err != nil {
			return nil, err
		}
		bookings = append(bookings, b)
	}
	return bookings, rows.Err()
}

func (d *DB) GetBookingByRef(ref string) (*Booking, error) {
	row := d.conn.QueryRow(`SELECT id,ref,type,first_name,last_name,email,phone,
	  from_code,to_code,airline,depart_time,arrive_time,duration,stops,
	  hotel_name,checkin,checkout,price,currency,card_last4,created_at
	FROM bookings WHERE ref=?`, ref)
	b := &Booking{}
	return b, row.Scan(
		&b.ID, &b.Ref, &b.Type, &b.FirstName, &b.LastName, &b.Email, &b.Phone,
		&b.FromCode, &b.ToCode, &b.Airline, &b.DepartTime, &b.ArriveTime, &b.Duration, &b.Stops,
		&b.HotelName, &b.Checkin, &b.Checkout, &b.Price, &b.Currency, &b.CardLast4, &b.CreatedAt,
	)
}

func generateRef() string {
	const chars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	var sb strings.Builder
	sb.WriteString("TM-")
	for i := 0; i < 6; i++ {
		sb.WriteByte(chars[r.Intn(len(chars))])
	}
	return sb.String()
}
