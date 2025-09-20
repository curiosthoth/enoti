package flow

func (s *UnitTestSuite) TestEvalAny() {
	// Test the JMESPath evaluation
	obj := map[string]any{
		"key1": "value1",
		"key2": map[string]any{
			"subkey1": "subvalue1",
			"subkey2": 42,
		},
		"key3": []any{"elem1", "elem2", "elem3"},
		"key4": nil,
	}

	v, err := EvalAny("key1", obj)
	s.NoError(err)
	s.Equal("value1", v.(string))

	v, err = EvalAny("key2.subkey1", obj)
	s.NoError(err)
	s.Equal("subvalue1", v.(string))

	v, err = EvalAny("key2.subkey2", obj)
	s.NoError(err)
	s.Equal(42, v.(int))

	v, err = EvalAny("key3[0]", obj)
	s.NoError(err)
	s.Equal("elem1", v.(string))

	v, err = EvalAny("key3[1]", obj)
	s.NoError(err)
	s.Equal("elem2", v.(string))

	v, err = EvalAny("key4", obj)
	s.NoError(err)
	s.Nil(v)

	v, err = EvalAny("nonexistent", obj)
	s.NoError(err)
	s.Nil(v)

	// Test `contains`
	v, err = EvalAny("contains(key3, 'elem2')", obj)
	s.NoError(err)
	s.Equal(true, v.(bool))

	v, err = EvalAny("contains(key3, 'elemX')", obj)
	s.NoError(err)
	s.Equal(false, v.(bool))
}
