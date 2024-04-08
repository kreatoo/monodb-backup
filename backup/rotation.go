package backup

import (
	"monodb-backup/config"
	"strconv"
	"time"
)

type rightNow struct {
	year   string
	month  string
	day    string
	hour   string
	minute string
	now    string
}

var dateNow rightNow

func dumpName(db string, rotation config.Rotation, buName string) string {
	if !rotation.Enabled {
		date := rightNow{
			year:  time.Now().Format("2006"),
			month: time.Now().Format("01"),
			now:   time.Now().Format("2006-01-02-150405"),
		}
		var name string
		if !params.BackupAsTables || db == "mysql_users" {
			name = date.year + "/" + date.month + "/" + db + "-" + date.now
		} else {
			name = date.year + "/" + date.month + "/" + db + "/" + buName + "-" + date.now
		}
		return name
	} else {
		suffix := rotation.Suffix
		if !params.BackupAsTables {
			switch suffix {
			case "day":
				return db + "-" + dateNow.day
			case "hour":
				return db + "-" + dateNow.hour
			case "minute":
				return db + "-" + dateNow.minute
			default:
				return db + "-" + dateNow.day
			}
		} else if params.Minio.S3FS.ShouldMount {
			if db == "mysql_users" {
				db = "mysql"
				buName = "mysql_users"
			}
			switch suffix { //TODO + db + "/" +
			case "day":
				return buName + "-" + dateNow.day
			case "hour":
				return buName + "-" + dateNow.hour
			case "minute":
				return buName + "-" + dateNow.minute
			default:
				return buName + "-" + dateNow.day
			}
		} else {
			if db == "mysql_users" {
				db = "mysql"
				buName = "mysql_users"
			}
			switch suffix { //TODO + db + "/" +
			case "day":
				return db + "/" + buName + "-" + dateNow.day
			case "hour":
				return db + "/" + buName + "-" + dateNow.hour
			case "minute":
				return db + "/" + buName + "-" + dateNow.minute
			default:
				return db + "/" + buName + "-" + dateNow.day
			}
		}
	}
}

func rotate(db string) (bool, string) {
	t := time.Now()
	_, week := t.ISOWeek()
	date := rightNow{
		month: time.Now().Format("Jan"),
		day:   time.Now().Format("Mon"),
	}
	switch config.Parameters.Rotation.Period {
	case "month":
		yesterday := t.AddDate(0, 0, -1)
		if yesterday.Month() != t.Month() {
			return true, "Monthly/" + db + "-" + date.month
		}
	case "week":
		if date.day == "Mon" {
			return true, "Weekly/" + db + "-week_" + strconv.Itoa(week)
		}
	}
	return false, ""
}

func rotatePath() string {
	t := time.Now()
	date := rightNow{
		month: time.Now().Format("Jan"),
		day:   time.Now().Format("Mon"),
	}
	switch config.Parameters.Rotation.Period {
	case "month":
		yesterday := t.AddDate(0, 0, -1)
		if yesterday.Month() != t.Month() {
			return "Monthly/"
		}
	case "week":
		if date.day == "Mon" {
			return "Weekly/"
		}
	}
	return ""
}

func minioPath() (newName string) {
	if !params.Rotation.Enabled {
		date := rightNow{
			year:  time.Now().Format("2006"),
			month: time.Now().Format("01"),
			now:   time.Now().Format("2006-01-02-150405"),
		}
		newName = date.year + "/" + date.month
	} else {
		suffix := params.Rotation.Suffix
		switch suffix {
		case "day":
			newName = "Daily/" + dateNow.day
		case "hour":
			newName = "Hourly/" + dateNow.day + "/" + dateNow.hour
		case "minute":
			newName = "Custom/" + dateNow.day + "/" + dateNow.hour
		default:
			newName = "Daily/" + dateNow.day
		}
	}
	return
}

func nameWithPath(name string) (newName string) {
	if !params.Rotation.Enabled {
		newName = name
	} else {
		suffix := params.Rotation.Suffix
		switch suffix {
		case "day":
			newName = "Daily/" + dateNow.day + "/" + name
		case "hour":
			newName = "Hourly/" + dateNow.day + "/" + dateNow.hour + "/" + name
		case "minute":
			newName = "Custom/" + dateNow.day + "/" + dateNow.hour + "/" + name
		default:
			newName = "Daily/" + name
		}
	}
	return
}
