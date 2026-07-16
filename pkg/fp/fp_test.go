package fp

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMap(t *testing.T) {
	result := Map([]int{1, 2, 3}, func(v int) string {
		return string(rune('0' + v))
	})
	assert.Equal(t, []string{"1", "2", "3"}, result)
}

func TestFilter(t *testing.T) {
	result := Filter([]int{1, 2, 3, 4, 5}, func(v int) bool {
		return v%2 == 0
	})
	assert.Equal(t, []int{2, 4}, result)
}

func TestUnique(t *testing.T) {
	result := Unique([]string{"a", "b", "a", "c", "b"})
	assert.Equal(t, []string{"a", "b", "c"}, result)
	assert.Nil(t, Unique[int](nil))
}

func TestFlatMap(t *testing.T) {
	result := FlatMap([]int{1, 2, 3}, func(v int) []int {
		return []int{v, v * 10}
	})
	assert.Equal(t, []int{1, 10, 2, 20, 3, 30}, result)
}

func TestReduce(t *testing.T) {
	sum := Reduce([]int{1, 2, 3, 4}, 0, func(acc, v int) int {
		return acc + v
	})
	assert.Equal(t, 10, sum)
}

func TestAnyAndAll(t *testing.T) {
	anyEven := Any([]int{1, 3, 4}, func(v int) bool {
		return v%2 == 0
	})
	allPositive := All([]int{1, 3, 4}, func(v int) bool {
		return v > 0
	})
	assert.True(t, anyEven)
	assert.True(t, allPositive)
}

func TestFind(t *testing.T) {
	value, ok := Find([]int{5, 7, 8, 9}, func(v int) bool {
		return v%2 == 0
	})
	assert.True(t, ok)
	assert.Equal(t, 8, value)

	missing, ok := Find([]int{1, 3, 5}, func(v int) bool {
		return v%2 == 0
	})
	assert.False(t, ok)
	assert.Equal(t, 0, missing)
}

func TestMapErr(t *testing.T) {
	result, err := MapErr([]int{1, 2, 3}, func(v int) (int, error) {
		if v == 2 {
			return 0, errors.New("boom")
		}
		return v * 10, nil
	})
	assert.Error(t, err)
	assert.Nil(t, result)

	result, err = MapErr([]int{1, 2, 3}, func(v int) (int, error) {
		return v * 10, nil
	})
	assert.NoError(t, err)
	assert.Equal(t, []int{10, 20, 30}, result)
}

func TestKeysWhere(t *testing.T) {
	keys := KeysWhere(map[int]string{1: "a", 2: "bb", 3: "ccc"}, func(k int, v string) bool {
		return len(v) >= 2
	})
	assert.ElementsMatch(t, []int{2, 3}, keys)
}

func TestValuesWhere(t *testing.T) {
	values := ValuesWhere(map[int]string{1: "a", 2: "bb", 3: "ccc"}, func(k int, v string) bool {
		return k%2 == 1
	})
	assert.ElementsMatch(t, []string{"a", "ccc"}, values)
}

func TestFilterByKey(t *testing.T) {
	result := FilterByKey(map[int]string{1: "a", 2: "bb", 3: "ccc"}, func(k int) bool {
		return k >= 2
	})
	assert.Equal(t, map[int]string{2: "bb", 3: "ccc"}, result)
}

func TestFilterByValue(t *testing.T) {
	result := FilterByValue(map[int]string{1: "a", 2: "bb", 3: "ccc"}, func(v string) bool {
		return len(v) >= 2
	})
	assert.Equal(t, map[int]string{2: "bb", 3: "ccc"}, result)
}

func TestNilInputBehavior(t *testing.T) {
	assert.Nil(t, Map[int, int](nil, func(v int) int { return v * 2 }))
	assert.Nil(t, Filter[int](nil, func(v int) bool { return v%2 == 0 }))
	assert.Nil(t, FlatMap[int, int](nil, func(v int) []int { return []int{v} }))
	assert.Nil(t, KeysWhere[int, int](nil, func(k, v int) bool { return true }))
	assert.Nil(t, ValuesWhere[int, int](nil, func(k, v int) bool { return true }))
	assert.Nil(t, FilterByKey[int, int](nil, func(k int) bool { return true }))
	assert.Nil(t, FilterByValue[int, int](nil, func(v int) bool { return true }))
}
