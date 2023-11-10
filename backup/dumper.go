package backup

import (
	"bytes"
	"os"
	"os/exec"
	"pgsql-backup/config"
	"pgsql-backup/notify"
	"strings"
	"time"
)

type Dumper struct {
	p *config.Params
	m *notify.Mattermost
	l Logger
}

type backupStatus struct {
	s3    uploadStatus
	minio uploadStatus
}

type uploadStatus struct {
	enabled bool
	success bool
	msg     string
}

type rightNow struct {
	year  string
	month string
	now   string
}

// NewDumper creates a new Dumper instance.
func NewDumper(params *config.Params, logger Logger) (d *Dumper) {
	d = &Dumper{
		p: params,
		m: notify.NewMattermost(params),
		l: logger,
	}
	return
}

func (d *Dumper) reportLog(message string, boolean bool) {
	d.l.Info(message)
	d.m.Notify(message, "", "", boolean)
}

func (d *Dumper) getDBList() []string {
	cmd := exec.Command("/usr/bin/psql", "-lqt")
	out, err := cmd.Output()
	if err != nil {
		d.reportLog("Could not get database list: "+err.Error(), true)
		return nil
	}

	var dbList []string
	for _, line := range bytes.Split(out, []byte{'\n'}) {
		if len(line) > 0 {
			ln := strings.TrimSpace(strings.Split(string(line), "|")[0])
			if ln == "" || ln == "template0" || ln == "template1" || ln == "postgres" {
				continue
			}
			dbList = append(dbList, ln)
		}
	}
	return dbList
}

func (d *Dumper) Dump() {

	d.reportLog("PostgreSQL database backup started.", false)

	if len(d.p.Databases) == 0 {

		d.reportLog("Getting database list...", false)
		d.p.Databases = d.getDBList()
	}

	for _, db := range d.p.Databases {
		d.dumpSingleDb(db, d.p.BackupDestination)
	}

	d.reportLog("PostgreSQL database backup finished.", false)
}

func (d *Dumper) dumpSingleDb(db string, dst string) {
	dfp := dst + "/" + db + ".sql"
	date := rightNow{
		year:  time.Now().Format("2006"),
		month: time.Now().Format("01"),
		now:   time.Now().Format("2006-01-02-150405"),
	}
	_ = os.MkdirAll(dst+"/"+date.year+"/"+date.month, os.ModePerm)
	tfp := dst + "/" + date.year + "/" + date.month + "/" + db + "--" + date.now + ".tar.7z"

	logInfo := map[string]interface{}{
		"db":                       db,
		"dump location":            dfp,
		"compressed file location": tfp,
	}

	d.l.InfoWithFields(logInfo, "Database is being backed up...")

	cmd := exec.Command("/usr/bin/pg_dump", db)
	f, err := os.Create(dfp)
	if err != nil {
		logInfo["error"] = err.Error()

		d.l.ErrorWithFields(logInfo, "Output file could not be created.")

		d.m.Notify(
			"An error occurred while backing up the "+db+" database.",
			"Output file could not be created:",
			err.Error(),
			true,
		)
		return
	}

	defer f.Close()
	defer func() {
		err = os.Remove(dfp)
	}()

	cmd.Stdout = f

	err = cmd.Start()
	if err != nil {
		logInfo["error"] = err.Error()

		d.l.ErrorWithFields(logInfo, "Dump could not be taken.")

		d.m.Notify(
			"An error occurred while backing up the "+db+" database.",
			"Dump could not be taken to the "+dfp+" file:",
			err.Error(),
			true,
		)
		notify.Email(d.p, "Backup error", "An error occurred while backing up the "+db+" database. Dump could not be taken to the "+dfp+" file: "+err.Error(), true)
		return
	}
	err = cmd.Wait()
	if err != nil {
		logInfo["error"] = err.Error()

		d.l.ErrorWithFields(logInfo, "Dump could not be taken.")

		d.m.Notify(
			"An error occurred while backing up the "+db+" database.",
			"Dump could not be taken to the "+dfp+" file:",
			err.Error(),
			true,
		)
		notify.Email(d.p, "Backup error", "An error occurred while backing up the "+db+" database. Dump could not be taken to the "+dfp+" file: "+err.Error(), true)
		return
	}

	cmd = exec.Command("tar", "cf", "-", dfp)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		logInfo["error"] = err.Error()

		d.l.ErrorWithFields(logInfo, "Dump file could not be archived.")

		d.m.Notify(
			"An error occurred while backing up the "+db+" database.",
			"Dump file "+dfp+" could not be archived to the "+tfp+" target.",
			err.Error(),
			true,
		)
		notify.Email(d.p, "Backup error", "An error occurred while backing up the "+db+" database. Dump file "+dfp+" could not be archived to the "+tfp+" target: "+err.Error(), true)
		return
	}

	err = cmd.Start()
	if err != nil {
		logInfo["error"] = err.Error()

		d.l.ErrorWithFields(logInfo, "Dump file could not be archived.")

		d.m.Notify(
			"An error occurred while backing up the "+db+" database.",
			"Dump file "+dfp+" could not be archived to the "+tfp+" target.",
			err.Error(),
			true,
		)
		notify.Email(d.p, "Backup error", "An error occurred while backing up the "+db+" database. Dump file "+dfp+" could not be archived to the "+tfp+" target: "+err.Error(), true)
		return
	}

	cmd2 := exec.Command("7z", "a", "-t7z", "-ms=on", "-mhe=on", "-p"+d.p.ArchivePass, "-si", tfp)
	cmd2.Stdin = stdout

	err = cmd2.Run()
	if err != nil {
		logInfo["error"] = err.Error()

		d.l.ErrorWithFields(logInfo, "Dump file could not be archived.")

		d.m.Notify(
			"An error occurred while backing up the "+db+" database.",
			"Dump file "+dfp+" could not be archived to the "+tfp+" target.",
			err.Error(),
			true,
		)
		notify.Email(d.p, "Backup error", "An error occurred while backing up the "+db+" database. Dump file "+dfp+" could not be archived to the "+tfp+" target: "+err.Error(), true)
		return
	}

	d.l.InfoWithFields(logInfo, "Database backed up.")

	d.m.Notify("The "+db+" database was backed up to the "+tfp+" location.", "", "", false)

	backupStatus := backupStatus{
		s3: uploadStatus{
			enabled: false,
			success: true,
			msg:     "",
		},
		minio: uploadStatus{
			enabled: false,
			success: true,
			msg:     "",
		},
	}

	if d.p.S3.Enabled {
		d.uploadToS3(db, dfp, tfp, &backupStatus, date)
	}

	if d.p.Minio.Enabled {
		d.uploadToMinio(db, dfp, tfp, &backupStatus, date)
	}

	if !backupStatus.minio.enabled && !backupStatus.s3.enabled {

		err = notify.Email(d.p, "Backup successful", "The "+db+" database was backed up to the "+tfp+" location.", false)
		if err != nil {

			d.l.Error("Mail could not be sent: " + err.Error())
		}
		return
	}

	if d.p.RemoveLocal {
		_ = os.Remove(tfp)
	}
}

func (d *Dumper) uploadToS3(db, dfp, tfp string, backupStatus *backupStatus, date rightNow) {

	logInfoS3 := map[string]interface{}{
		"db":                       db,
		"dump location":            dfp,
		"compressed file location": tfp,
		"s3Bucket":                 d.p.S3.Bucket,
		"s3Path":                   d.p.S3.Path,
		"s3Region":                 d.p.S3.Region,
	}
	d.l.InfoWithFields(logInfoS3, "Database backup is being uploaded to S3...")

	backupStatus.s3.enabled = true

	uploader, err := newS3Uploader(d.p.S3.Region, d.p.S3.AccessKey, d.p.S3.SecretKey)
	if err != nil {
		logInfoS3["error"] = err.Error()

		d.l.ErrorWithFields(logInfoS3, "Database backup could not be uploaded to S3.")

		d.m.Notify("Connection could not be established to upload the "+db+" database backup to S3.", "", err.Error(), true)
		backupStatus.s3.success = false

		backupStatus.s3.msg = "Connection could not be established to upload the " + db + " database backup to S3: " + err.Error()

		return
	} else {
		target := date.year + "/" + date.month + "/" + db + "--" + date.now + ".tar.7z"
		if d.p.S3.Path != "" {
			target = d.p.S3.Path + "/" + target
		}
		logInfoS3["target"] = target
		err = uploadFileToS3(uploader, tfp, d.p.S3.Bucket, target)
		if err != nil {
			logInfoS3["error"] = err.Error()

			d.l.ErrorWithFields(logInfoS3, "Database backup could not be uploaded to S3.")

			d.m.Notify("The "+db+" database backup could not be uploaded to S3.", "", err.Error(), true)
			backupStatus.s3.success = false

			backupStatus.s3.msg = "The " + db + " database backup could not be uploaded to S3: " + err.Error()
		} else {

			d.l.InfoWithFields(logInfoS3, "Database backup uploaded to S3.")

			backupStatus.s3.success = true

			backupStatus.s3.msg = "The " + db + " database backup was uploaded to S3."
		}
	}

	subject := "Backup successful"
	body := db + " database backed up to " + tfp + " location."
	if backupStatus.s3.success {
		subject += ", upload to S3 successful"
	} else {
		subject += ", upload to S3 failed"
	}
	body += " " + backupStatus.s3.msg

	err = notify.Email(d.p, subject, body, !backupStatus.s3.success)
	if err != nil {

		d.l.Error("Mail could not be sent: " + err.Error())
	}
}

func (d *Dumper) uploadToMinio(db, dfp, tfp string, backupStatus *backupStatus, date rightNow) {

	logInfoMinio := map[string]interface{}{
		"db":                       db,
		"dump location":            dfp,
		"compressed file location": tfp,
		"minioEndpoint":            d.p.Minio.Endpoint,
		"minioBucket":              d.p.Minio.Bucket,
		"minioPath":                d.p.Minio.Path,
	}

	d.l.InfoWithFields(logInfoMinio, "Database backup is being uploaded to MinIO...")

	backupStatus.minio.enabled = true

	minioClient, err := newMinioClient(
		d.p.Minio.Endpoint,
		d.p.Minio.AccessKey,
		d.p.Minio.SecretKey,
		d.p.Minio.Secure,
		d.p.Minio.InsecureSkipVerify)
	if err != nil {
		logInfoMinio["error"] = err.Error()

		d.l.ErrorWithFields(logInfoMinio, "Database backup could not be uploaded to MinIO.")

		d.m.Notify("Connection could not be established to upload the "+db+" database backup to MinIO.", "", err.Error(), true)
		backupStatus.minio.success = false

		backupStatus.minio.msg = "Connection could not be established to upload the " + db + " database backup to MinIO: " + err.Error()
	} else {
		target := date.year + "/" + date.month + "/" + db + "--" + date.now + ".tar.7z"
		if d.p.Minio.Path != "" {
			target = d.p.Minio.Path + "/" + target
		}
		logInfoMinio["target"] = target
		err = uploadFileToMinio(minioClient, tfp, d.p.Minio.Bucket, target)
		if err != nil {
			logInfoMinio["error"] = err.Error()

			d.l.ErrorWithFields(logInfoMinio, "Database backup could not be uploaded to MinIO.")

			d.m.Notify("The "+db+" database backup could not be uploaded to MinIO", "", err.Error(), true)
			backupStatus.minio.success = false

			backupStatus.minio.msg = db + " database backup could not be uploaded to MinIO: " + err.Error()
		} else {

			d.l.InfoWithFields(logInfoMinio, "Database backup uploaded to MinIO.")

			d.m.Notify(db+" database was uploaded to MinIO.", "", "", false)
			backupStatus.minio.success = true

			backupStatus.minio.msg = db + " database backup uploaded to MinIO."
		}
	}

	subject := "Backup successful"
	body := db + " database " + tfp + " location backed up."
	if backupStatus.minio.success {

		subject += ", Uploaded to MinIO successfully"
	} else {

		subject += ", Failed to upload to MinIO"
	}
	body += " " + backupStatus.minio.msg

	err = notify.Email(d.p, subject, body, !backupStatus.minio.success)
	if err != nil {

		d.l.Error("Mail could not be sent: " + err.Error())
	}

}
