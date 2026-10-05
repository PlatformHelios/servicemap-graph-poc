package graph

import (
	"context"
	"fmt"

	"github.com/PlatformHelios/servicemap-graph-poc/internal/cmdb"
)

func (s *Store) ListHolidays(ctx context.Context) ([]cmdb.Holiday, error) {
	records, err := s.execute(ctx, false, "MATCH (holiday:Holiday) RETURN holiday.date AS date, coalesce(holiday.name, '') AS name ORDER BY holiday.date", nil)
	if err != nil {
		return nil, fmt.Errorf("list holidays: %w", err)
	}
	holidays := make([]cmdb.Holiday, 0, len(records))
	for _, record := range records {
		holidays = append(holidays, cmdb.Holiday{Date: fmt.Sprint(record.Values[0]), Name: fmt.Sprint(record.Values[1])})
	}
	return holidays, nil
}

func (s *Store) SaveHolidays(ctx context.Context, holidays []cmdb.Holiday) error {
	if len(holidays) == 0 {
		if _, err := s.execute(ctx, true, "MATCH (holiday:Holiday) DELETE holiday", nil); err != nil {
			return fmt.Errorf("save holidays: %w", err)
		}
		return nil
	}
	dates := make([]string, 0, len(holidays))
	names := make([]string, 0, len(holidays))
	for _, holiday := range holidays {
		dates = append(dates, holiday.Date)
		names = append(names, holiday.Name)
	}
	_, err := s.execute(ctx, true, "UNWIND range(0, size($dates)-1) AS index MERGE (holiday:Holiday {date: $dates[index]}) SET holiday.name = $names[index] WITH collect(holiday.date) AS kept MATCH (holiday:Holiday) WHERE NOT holiday.date IN kept DELETE holiday", map[string]any{"dates": dates, "names": names})
	if err != nil {
		return fmt.Errorf("save holidays: %w", err)
	}
	return nil
}
