package maxinflight

import (
	"github.com/concourse/atc"
	"github.com/concourse/atc/db"
	"github.com/pivotal-golang/lager"
)

//go:generate counterfeiter . MaxInFlightUpdater

type MaxInFlightUpdater interface {
	UpdateMaxInFlightReached(logger lager.Logger, jobConfig atc.JobConfig, buildID int) (bool, error)
}

//go:generate counterfeiter . MaxInFlightUpdaterDB

type MaxInFlightUpdaterDB interface {
	GetRunningBuildsBySerialGroup(jobName string, serialGroups []string) ([]db.Build, error)
	GetNextPendingBuildBySerialGroup(jobName string, serialGroups []string) (db.Build, bool, error)
	SetMaxInFlightReached(jobName string, reached bool) error
}

func NewMaxInFlightUpdater(db MaxInFlightUpdaterDB) MaxInFlightUpdater {
	return &maxInFlightUpdater{db: db}
}

type maxInFlightUpdater struct {
	db MaxInFlightUpdaterDB
}

func (s *maxInFlightUpdater) UpdateMaxInFlightReached(logger lager.Logger, jobConfig atc.JobConfig, buildID int) (bool, error) {
	logger = logger.Session("is-max-in-flight-reached")
	maxInFlight := jobConfig.MaxInFlight()

	if maxInFlight == 0 {
		return false, nil
	}

	builds, err := s.db.GetRunningBuildsBySerialGroup(jobConfig.Name, jobConfig.GetSerialGroups())
	if err != nil {
		logger.Error("failed-to-get-running-builds-by-serial-group", err)
		return false, err
	}

	if len(builds) >= maxInFlight {
		return true, nil
	}

	nextMostPendingBuild, found, err := s.db.GetNextPendingBuildBySerialGroup(jobConfig.Name, jobConfig.GetSerialGroups())
	if err != nil {
		logger.Error("failed-to-get-next-pending-build-by-serial-group", err)
		return false, err
	}

	if !found {
		logger.Info("pending-build-disappeared-from-serial-group")
		return true, nil
	}

	return nextMostPendingBuild.ID != buildID, nil
}
