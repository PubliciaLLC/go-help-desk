package user

// SetCompareHook replaces the bcrypt comparison so a test can see which
// hashes a sign-in spent work on. The hook must still return the real result.
func SetCompareHook(s *Service, f func(hash, plain []byte) error) { s.compare = f }

// DummyPassword is the plaintext behind the dummy hash, so a test can prove it
// never signs anyone in.
const DummyPassword = dummyPassword
