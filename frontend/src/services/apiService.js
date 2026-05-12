import axios from 'axios';

const BASE_URL = 'http://localhost:8080';

/**
 * Search hotels by city name.
 * Calls the Go backend which proxies to Booking.com API.
 */
export const searchHotels = async (city = 'London', checkin = '2026-06-01', checkout = '2026-06-05') => {
  const response = await axios.get(`${BASE_URL}/travel-data`, {
    params: { q: city, checkin, checkout },
  });
  return response.data;
};

/**
 * Fetch default London hotels (no search params).
 */
export const fetchDefaultHotels = async () => {
  const response = await axios.get(`${BASE_URL}/travel-data`);
  return response.data;
};
