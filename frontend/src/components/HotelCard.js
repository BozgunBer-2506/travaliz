import React from 'react';

/**
 * Displays a single hotel card.
 * Separating UI from logic makes this unit-testable with Jest/Wallaby independently.
 *
 * @param {Object}   hotel   - Hotel data object from the API
 * @param {Function} onBook  - Callback fired when "Book Now" is clicked
 */
const HotelCard = ({ hotel, onBook }) => {
  const handleBook = () => onBook(hotel);

  return (
    <div className="hotel-card" data-testid="hotel-card">
      <div className="hotel-card__image">
        {hotel.photo_url ? (
          <img
            src={hotel.photo_url}
            alt={hotel.hotel_name}
            onError={(e) => { e.target.style.display = 'none'; }}
          />
        ) : (
          <div className="hotel-card__image--placeholder" />
        )}
        {hotel.stars > 0 && (
          <span className="hotel-card__stars">{hotel.stars}★</span>
        )}
      </div>

      <div className="hotel-card__body">
        <h3 className="hotel-card__name">{hotel.hotel_name}</h3>

        <div className="hotel-card__footer">
          <div className="hotel-card__price">
            <span className="hotel-card__price--amount">
              ${hotel.price?.toFixed(0)}
            </span>
            <span className="hotel-card__price--label">/night</span>
          </div>

          {hotel.rating > 0 && (
            <div className="hotel-card__rating" data-testid="hotel-rating">
              <span>★ {hotel.rating?.toFixed(1)}</span>
              <span className="hotel-card__rating--word">{hotel.rating_word}</span>
            </div>
          )}
        </div>

        <button
          className="hotel-card__book-btn"
          onClick={handleBook}
          data-testid="book-button"
        >
          Book Now
        </button>
      </div>
    </div>
  );
};

export default HotelCard;
