import React, { useState, useEffect, useCallback } from 'react';
import HotelCard from './components/HotelCard';
import { searchHotels } from './services/apiService';

const DEFAULT_CHECKIN  = '2026-06-01';
const DEFAULT_CHECKOUT = '2026-06-05';

const App = () => {
  // Search state
  const [city,     setCity]     = useState('London');
  const [checkin,  setCheckin]  = useState(DEFAULT_CHECKIN);
  const [checkout, setCheckout] = useState(DEFAULT_CHECKOUT);
  const [guests,   setGuests]   = useState(2);

  // Data state
  const [hotels,   setHotels]   = useState([]);
  const [bookings, setBookings] = useState([]);

  // UI state
  const [loading,      setLoading]      = useState(false);
  const [error,        setError]        = useState(null);
  const [notification, setNotification] = useState(null);

  // Fetch hotels - separated as a named function so it can be unit-tested
  const fetchHotels = useCallback(async (searchCity, searchCheckin, searchCheckout) => {
    setLoading(true);
    setError(null);
    try {
      const data = await searchHotels(searchCity, searchCheckin, searchCheckout);
      setHotels(data ?? []);
    } catch {
      setError('Hizmet şu an kapalı. Lütfen daha sonra tekrar deneyin.');
      setHotels([]);
    } finally {
      setLoading(false);
    }
  }, []);

  // Load default results on mount
  useEffect(() => {
    fetchHotels(city, checkin, checkout);
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  const handleSearch = (e) => {
    e.preventDefault();
    fetchHotels(city, checkin, checkout);
  };

  // Book a hotel - pure logic, easily unit-testable
  const handleBook = (hotel) => {
    const booking = {
      id:       Date.now(),
      hotel,
      checkin,
      checkout,
      guests,
      bookedAt: new Date().toISOString(),
    };
    setBookings((prev) => [...prev, booking]);
    showNotification(`Başarıyla Rezervasyon Yapıldı: ${hotel.hotel_name}`);
  };

  const showNotification = (message) => {
    setNotification(message);
    setTimeout(() => setNotification(null), 3500);
  };

  return (
    <div className="app">

      {/* Header */}
      <header className="app__header">
        <h1>TravelMirror</h1>
        <p>Discover hotels around the world</p>
      </header>

      {/* Search Form */}
      <section className="app__search">
        <form onSubmit={handleSearch} className="search-form">
          <input
            type="text"
            value={city}
            onChange={(e) => setCity(e.target.value)}
            placeholder="Where do you want to go?"
            aria-label="City"
          />
          <input
            type="date"
            value={checkin}
            onChange={(e) => setCheckin(e.target.value)}
            aria-label="Check-in date"
          />
          <input
            type="date"
            value={checkout}
            onChange={(e) => setCheckout(e.target.value)}
            aria-label="Check-out date"
          />
          <input
            type="number"
            value={guests}
            min={1}
            max={10}
            onChange={(e) => setGuests(Number(e.target.value))}
            aria-label="Number of guests"
          />
          <button type="submit" disabled={loading}>
            {loading ? 'Searching...' : 'Search'}
          </button>
        </form>
      </section>

      {/* Notification */}
      {notification && (
        <div className="notification" role="alert" data-testid="notification">
          {notification}
        </div>
      )}

      {/* Error */}
      {error && (
        <div className="error-banner" role="alert" data-testid="error-banner">
          {error}
        </div>
      )}

      {/* Hotel Grid */}
      <main className="app__main">
        <div className="app__results-header">
          <h2>Hotels in {city}</h2>
          {!loading && <span>{hotels.length} properties found</span>}
        </div>

        {loading ? (
          <div className="loading-spinner" data-testid="loading">Loading...</div>
        ) : (
          <div className="hotels-grid" data-testid="hotels-grid">
            {hotels.map((hotel) => (
              <HotelCard
                key={hotel.hotel_id}
                hotel={hotel}
                onBook={handleBook}
              />
            ))}
          </div>
        )}
      </main>

      {/* Bookings Panel */}
      {bookings.length > 0 && (
        <aside className="bookings-panel" data-testid="bookings-panel">
          <h2>Your Bookings ({bookings.length})</h2>
          <ul>
            {bookings.map((b) => (
              <li key={b.id}>
                <strong>{b.hotel.hotel_name}</strong> &mdash; {b.checkin} to {b.checkout} &mdash; {b.guests} guest(s)
              </li>
            ))}
          </ul>
        </aside>
      )}

      <footer className="app__footer">
        <p>TravelMirror &copy; 2026</p>
      </footer>
    </div>
  );
};

export default App;
