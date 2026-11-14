package nested

func Loop(s *acc, n int) {
	for i := 0; i < n; i++ {
		s.Step(i)
	}
}

type acc struct{ a, b, c, d int }

func (s *acc) Step(v int) { s.step(v) }

func (s *acc) step(v int) {
	switch v & 7 {
	case 0:
		s.a += v
	case 1:
		s.b ^= v
	case 2:
		s.c -= v
	case 3:
		s.d += v >> 1
	case 4:
		s.a, s.b = s.b, s.a
	case 5:
		s.c, s.d = s.d+1, s.c-1
	case 6:
		s.a = s.a*3 + v
	default:
		s.b = s.b*5 - v
	}
	if s.a > 1<<40 {
		s.a = 0
	}
	if s.c < -1<<40 {
		s.c = 0
	}
}
