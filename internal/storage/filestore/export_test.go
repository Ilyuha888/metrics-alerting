package filestore

func (s *Storage) CountSyncSaves() *int {
	count := new(int)
	save := s.syncSave
	s.syncSave = func() error {
		*count++
		return save()
	}
	return count
}
