package flow

import "time"

func (s *UnitTestSuite) TestTTLCache() {
	c := NewTTL[string, string]()
	c.Set("key1", "value1", 200*time.Millisecond)
	v, ok := c.Get("key1")
	s.True(ok)
	s.Equal("value1", v)

	time.Sleep(250 * time.Millisecond)
	v, ok = c.Get("key1")
	s.False(ok)
	s.Equal("", v)

}
