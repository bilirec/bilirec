package utils

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseRoomIDsFromQuery(t *testing.T) {
	ids, err := ParseRoomIDs("1, 2,3", nil)
	require.NoError(t, err)
	require.Equal(t, []int{1, 2, 3}, ids)
}

func TestParseRoomIDsFromBodyArray(t *testing.T) {
	ids, err := ParseRoomIDs("1,2", []byte(`[1,2,3]`))
	require.NoError(t, err)
	require.Equal(t, []int{1, 2, 3}, ids)
}

func TestParseRoomIDsFromBodyObject(t *testing.T) {
	ids, err := ParseRoomIDs("1,2", []byte(`{"roomIDs":"4,5,6"}`))
	require.NoError(t, err)
	require.Equal(t, []int{4, 5, 6}, ids)
}

func TestParseRoomIDsBodyError(t *testing.T) {
	_, err := ParseRoomIDs("1,2", []byte(`{"roomIDs":`))
	require.Error(t, err)
}

func TestParseDelimitedListStrings(t *testing.T) {
	values, err := ParseDelimitedList("tags", "alpha, beta", []byte(``), nil, func(raw any) (string, error) {
		value, ok := raw.(string)
		if !ok {
			return "", nil
		}
		return value, nil
	})
	require.NoError(t, err)
	require.Equal(t, []string{"alpha", "beta"}, values)
}

func TestParseIntList(t *testing.T) {
	values, err := ParseIntList("ids", "7, 8", nil)
	require.NoError(t, err)
	require.Equal(t, []int{7, 8}, values)
}
