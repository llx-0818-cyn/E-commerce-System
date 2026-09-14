package checkout

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"
)

var (
	ErrAddressValidation = errors.New("invalid address")
	ErrAddressNotFound   = errors.New("address not found")
)

func (s *Service) CreateAddress(ctx context.Context, accountID uint64, input CreateAddressInput) (*Address, error) {
	if accountID == 0 {
		return nil, ErrAddressValidation
	}
	normalized, err := normalizeAddress(input.AddressInput)
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	address := &Address{
		AccountID: accountID, RecipientName: normalized.RecipientName, Phone: normalized.Phone,
		Province: normalized.Province, City: normalized.City, District: normalized.District,
		Detail: normalized.Detail, PostalCode: normalized.PostalCode, IsDefault: input.IsDefault,
	}
	err = s.repository.transaction(ctx, func(tx *gorm.DB) error {
		if _, err := lockAddressesByAccount(ctx, tx, accountID); err != nil {
			return err
		}
		if input.IsDefault {
			if err := clearDefaultAddress(ctx, tx, accountID); err != nil {
				return err
			}
		}
		return createAddress(ctx, tx, address)
	})
	if err != nil {
		return nil, err
	}
	return address, nil
}

func (s *Service) UpdateAddress(ctx context.Context, accountID, addressID uint64, input AddressInput) (*Address, error) {
	if accountID == 0 || addressID == 0 {
		return nil, ErrAddressValidation
	}
	normalized, err := normalizeAddress(input)
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	err = s.repository.transaction(ctx, func(tx *gorm.DB) error {
		addresses, err := lockAddressesByAccount(ctx, tx, accountID)
		if err != nil {
			return err
		}
		if !containsAddress(addresses, addressID) {
			return ErrAddressNotFound
		}
		return updateAddressProfile(ctx, tx, accountID, addressID, normalized)
	})
	if err != nil {
		return nil, err
	}
	return s.GetAddress(ctx, accountID, addressID)
}

func (s *Service) SetDefaultAddress(ctx context.Context, accountID, addressID uint64) (*Address, error) {
	if accountID == 0 || addressID == 0 {
		return nil, ErrAddressValidation
	}
	if ctx == nil {
		ctx = context.Background()
	}
	err := s.repository.transaction(ctx, func(tx *gorm.DB) error {
		addresses, err := lockAddressesByAccount(ctx, tx, accountID)
		if err != nil {
			return err
		}
		if !containsAddress(addresses, addressID) {
			return ErrAddressNotFound
		}
		if err := clearDefaultAddress(ctx, tx, accountID); err != nil {
			return err
		}
		return setDefaultAddress(ctx, tx, accountID, addressID)
	})
	if err != nil {
		return nil, err
	}
	return s.GetAddress(ctx, accountID, addressID)
}

func (s *Service) DeleteAddress(ctx context.Context, accountID, addressID uint64) error {
	if accountID == 0 || addressID == 0 {
		return ErrAddressValidation
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return s.repository.transaction(ctx, func(tx *gorm.DB) error {
		addresses, err := lockAddressesByAccount(ctx, tx, accountID)
		if err != nil {
			return err
		}
		if !containsAddress(addresses, addressID) {
			return ErrAddressNotFound
		}
		return deleteOwnedAddress(ctx, tx, accountID, addressID)
	})
}

func (s *Service) ListAddresses(ctx context.Context, accountID uint64) ([]Address, error) {
	if accountID == 0 {
		return nil, ErrAddressValidation
	}
	return s.repository.listAddresses(ctx, accountID)
}

func (s *Service) GetAddress(ctx context.Context, accountID, addressID uint64) (*Address, error) {
	if accountID == 0 || addressID == 0 {
		return nil, ErrAddressValidation
	}
	address, err := s.repository.findOwnedAddress(ctx, accountID, addressID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrAddressNotFound
	}
	return address, err
}

func (s *Service) GetDefaultAddress(ctx context.Context, accountID uint64) (*Address, error) {
	if accountID == 0 {
		return nil, ErrAddressValidation
	}
	address, err := s.repository.findDefaultAddress(ctx, accountID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrAddressNotFound
	}
	return address, err
}

func containsAddress(addresses []Address, addressID uint64) bool {
	for _, address := range addresses {
		if address.ID == addressID {
			return true
		}
	}
	return false
}

func normalizeAddress(input AddressInput) (AddressInput, error) {
	input.RecipientName = strings.TrimSpace(input.RecipientName)
	input.Phone = strings.TrimSpace(input.Phone)
	input.Province = strings.TrimSpace(input.Province)
	input.City = strings.TrimSpace(input.City)
	input.District = strings.TrimSpace(input.District)
	input.Detail = strings.TrimSpace(input.Detail)
	input.PostalCode = strings.TrimSpace(input.PostalCode)
	if input.RecipientName == "" || utf8.RuneCountInString(input.RecipientName) > 100 ||
		input.Phone == "" || utf8.RuneCountInString(input.Phone) > 32 ||
		input.Province == "" || utf8.RuneCountInString(input.Province) > 100 ||
		input.City == "" || utf8.RuneCountInString(input.City) > 100 ||
		utf8.RuneCountInString(input.District) > 100 ||
		input.Detail == "" || utf8.RuneCountInString(input.Detail) > 500 ||
		utf8.RuneCountInString(input.PostalCode) > 32 {
		return AddressInput{}, fmt.Errorf("%w: address field is empty or exceeds limit", ErrAddressValidation)
	}
	return input, nil
}
