// Copyright 2014 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)
//
// Functions for creating and updating the elimination match schedule.

package main

import (
	"fmt"
	"math/rand"
	"strconv"
	"time"
)

const elimMatchSpacingSec = 600

// Incrementally creates any elimination matches that can be created, based on the results of alliance
// selection or prior elimination rounds. Returns the winning alliance once it has been determined.
func (database *Database) UpdateEliminationSchedule(startTime time.Time) ([]AllianceTeam, error) {
	alliances, err := database.GetAllAlliances()
	if err != nil {
		return []AllianceTeam{}, err
	}
	winner, err := database.buildEliminationMatchSet(1, 1, len(alliances))
	if err != nil {
		return []AllianceTeam{}, err
	}

	// Update the scheduled time for all matches that have yet to be run.
	matches, err := database.GetMatchesByType("elimination")
	if err != nil {
		return []AllianceTeam{}, err
	}
	matchIndex := 0
	for _, match := range matches {
		if match.Status == "complete" {
			continue
		}
		match.Time = startTime.Add(time.Duration(matchIndex*elimMatchSpacingSec) * time.Second)
		database.SaveMatch(&match)
		matchIndex++
	}

	return winner, err
}

// Recursively traverses the elimination bracket downwards, creating matches as necessary. Returns the winner
// of the given round if known.
func (database *Database) buildEliminationMatchSet(round int, group int, numAlliances int) ([]AllianceTeam, error) {
	if numAlliances < 2 {
		return []AllianceTeam{}, fmt.Errorf("Must have at least 2 alliances")
	}
	roundName, ok := map[int]string{1: "F", 2: "SF", 4: "QF", 8: "EF"}[round]
	if !ok {
		return []AllianceTeam{}, fmt.Errorf("Round of depth %d is not supported", round*2)
	}
	if round != 1 {
		roundName += strconv.Itoa(group)
	}

	// Recurse to figure out who the involved alliances are.
	var redAlliance, blueAlliance []AllianceTeam
	var err error
	if numAlliances < 4*round {
		// This is the first round for some or all alliances and will be at least partially populated from the
		// alliance selection results.
		matchups := []int{1, 16, 8, 9, 4, 13, 5, 12, 2, 15, 7, 10, 3, 14, 6, 11}
		factor := len(matchups) / round
		redAllianceNumber := matchups[(group-1)*factor]
		blueAllianceNumber := matchups[(group-1)*factor+factor/2]
		numDirectAlliances := 4*round - numAlliances
		if redAllianceNumber <= numDirectAlliances {
			// The red alliance has a bye or the number of alliances is a power of 2; get from alliance selection.
			redAlliance, err = database.GetTeamsByAlliance(redAllianceNumber)
			if err != nil {
				return []AllianceTeam{}, err
			}
		}
		if blueAllianceNumber <= numDirectAlliances {
			// The blue alliance has a bye or the number of alliances is a power of 2; get from alliance selection.
			blueAlliance, err = database.GetTeamsByAlliance(blueAllianceNumber)
			if err != nil {
				return []AllianceTeam{}, err
			}
		}
	}

	// If the alliances aren't known yet, get them from one round down in the bracket.
	if len(redAlliance) == 0 {
		redAlliance, err = database.buildEliminationMatchSet(round*2, group*2-1, numAlliances)
		if err != nil {
			return []AllianceTeam{}, err
		}
	}
	if len(blueAlliance) == 0 {
		blueAlliance, err = database.buildEliminationMatchSet(round*2, group*2, numAlliances)
		if err != nil {
			return []AllianceTeam{}, err
		}
	}

	// Bail if the rounds below are not yet complete and we don't know either alliance competing this round.
	if len(redAlliance) == 0 && len(blueAlliance) == 0 {
		return []AllianceTeam{}, nil
	}

	// Check if the match set exists already and if it has been won.
	var redWins, blueWins, numIncomplete int
	var ties []*Match
	matches, err := database.GetMatchesByElimRoundGroup(round, group)
	if err != nil {
		return []AllianceTeam{}, err
	}
	var unplayedMatches []*Match
	for _, match := range matches {
		// Update the teams in the match if they are not yet set or are incorrect.
		if len(redAlliance) != 0 && !(teamInAlliance(match.Red1, redAlliance) &&
			teamInAlliance(match.Red2, redAlliance) && teamInAlliance(match.Red3, redAlliance)) {
			shuffleRedTeams(&match, redAlliance)
			database.SaveMatch(&match)
		} else if len(blueAlliance) != 0 && !(teamInAlliance(match.Blue1, blueAlliance) &&
			teamInAlliance(match.Blue2, blueAlliance) && teamInAlliance(match.Blue3, blueAlliance)) {
			shuffleBlueTeams(&match, blueAlliance)
			database.SaveMatch(&match)
		}

		if match.Status != "complete" {
			unplayedMatches = append(unplayedMatches, &match)
			numIncomplete += 1
			continue
		}

		// Check who won.
		switch match.Winner {
		case "R":
			redWins += 1
		case "B":
			blueWins += 1
		case "T":
			ties = append(ties, &match)
		default:
			return []AllianceTeam{}, fmt.Errorf("Completed match %d has invalid winner '%s'", match.Id, match.Winner)
		}
	}

	// Delete any superfluous matches if the round is won.
	if redWins == 2 || blueWins == 2 {
		for _, match := range unplayedMatches {
			err = database.DeleteMatch(match)
			if err != nil {
				return []AllianceTeam{}, err
			}
		}

		// Bail out and announce the winner of this round.
		if redWins == 2 {
			return redAlliance, nil
		} else {
			return blueAlliance, nil
		}
	}

	// Create initial set of matches or recreate any superfluous matches that were deleted but now are needed
	// due to a revision in who won.
	if len(matches) == 0 || len(ties) == 0 && numIncomplete == 0 {
		// Fill in zeroes if only one alliance is known.
		if len(redAlliance) == 0 {
			redAlliance = []AllianceTeam{AllianceTeam{}, AllianceTeam{}, AllianceTeam{}}
		} else if len(blueAlliance) == 0 {
			blueAlliance = []AllianceTeam{AllianceTeam{}, AllianceTeam{}, AllianceTeam{}}
		}
		if len(redAlliance) < 3 || len(blueAlliance) < 3 {
			// Raise an error if the alliance selection process gave us less than 3 teams per alliance.
			return []AllianceTeam{}, fmt.Errorf("Alliances must consist of at least 3 teams")
		}
		if len(matches) < 1 {
			err = database.CreateMatch(createMatch(roundName, round, group, 1, redAlliance, blueAlliance))
			if err != nil {
				return []AllianceTeam{}, err
			}
		}
		if len(matches) < 2 {
			err = database.CreateMatch(createMatch(roundName, round, group, 2, redAlliance, blueAlliance))
			if err != nil {
				return []AllianceTeam{}, err
			}
		}
		if len(matches) < 3 {
			err = database.CreateMatch(createMatch(roundName, round, group, 3, redAlliance, blueAlliance))
			if err != nil {
				return []AllianceTeam{}, err
			}
		}
	}

	// Duplicate any ties if we have run out of matches. Don't reshuffle the team positions, so queueing
	// personnel can reuse any tied matches without having to print new schedules.
	if numIncomplete == 0 {
		for index, tie := range ties {
			match := createMatch(roundName, round, group, len(matches)+index+1, redAlliance, blueAlliance)
			match.Red1, match.Red2, match.Red3 = tie.Red1, tie.Red2, tie.Red3
			match.Blue1, match.Blue2, match.Blue3 = tie.Blue1, tie.Blue2, tie.Blue3
			err = database.CreateMatch(match)
			if err != nil {
				return []AllianceTeam{}, err
			}
		}
	}

	return []AllianceTeam{}, nil
}

// Creates a match at the given point in the elimination bracket and populates the teams.
func createMatch(roundName string, round int, group int, instance int, redAlliance []AllianceTeam, blueAlliance []AllianceTeam) *Match {
	match := Match{Type: "elimination", DisplayName: fmt.Sprintf("%s-%d", roundName, instance),
		ElimRound: round, ElimGroup: group, ElimInstance: instance}
	shuffleRedTeams(&match, redAlliance)
	shuffleBlueTeams(&match, blueAlliance)
	return &match
}

// Assigns the first three teams from the alliance randomly into the red team slots for the match.
func shuffleRedTeams(match *Match, alliance []AllianceTeam) {
	shuffle := rand.Perm(3)
	match.Red1 = alliance[shuffle[0]].TeamId
	match.Red2 = alliance[shuffle[1]].TeamId
	match.Red3 = alliance[shuffle[2]].TeamId
}

// Assigns the first three teams from the alliance randomly into the blue team slots for the match.
func shuffleBlueTeams(match *Match, alliance []AllianceTeam) {
	shuffle := rand.Perm(3)
	match.Blue1 = alliance[shuffle[0]].TeamId
	match.Blue2 = alliance[shuffle[1]].TeamId
	match.Blue3 = alliance[shuffle[2]].TeamId
}

// Returns true if the given team is part of the given alliance.
func teamInAlliance(teamId int, alliance []AllianceTeam) bool {
	for _, allianceTeam := range alliance {
		if teamId == allianceTeam.TeamId {
			return true
		}
	}
	return false
}
