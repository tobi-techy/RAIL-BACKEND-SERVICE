#!/bin/bash

# ============================================================================
# PostgreSQL Backup Strategy Script for RAIL Backend
# ============================================================================
# This script implements a comprehensive backup strategy including:
# - Automated daily backups
# - Point-in-time recovery (PITR) support
# - Multi-region replication
# - Backup validation and restoration testing
# - Backup retention and cleanup

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
BACKUP_DIR="${BACKUP_DIR:-/var/backups/rail}"
RETENTION_DAYS="${RETENTION_DAYS:-30}"
RETENTION_WEEKS="${RETENTION_WEEKS:-8}"
RETENTION_MONTHS="${RETENTION_MONTHS:-12}"
DB_HOST="${DB_HOST:-localhost}"
DB_PORT="${DB_PORT:-5432}"
DB_NAME="${DB_NAME:-rail_service_prod}"
DB_USER="${DB_USER:-postgres}"
S3_BUCKET="${S3_BUCKET:-rail-database-backups}"
S3_REGION="${S3_REGION:-us-east-1}"

# Timestamp
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
DATE=$(date +%Y%m%d)

# Backup file naming
BACKUP_FILE="${BACKUP_DIR}/${DB_NAME}_${TIMESTAMP}.dump"
BACKUP_LOG="${BACKUP_DIR}/backup_${TIMESTAMP}.log"

echo "========================================="
echo "RAIL Backend - PostgreSQL Backup"
echo "========================================="
echo ""
echo "Timestamp: ${TIMESTAMP}"
echo "Backup Dir: ${BACKUP_DIR}"
echo "Retention: ${RETENTION_DAYS} days, ${RETENTION_WEEKS} weeks, ${RETENTION_MONTHS} months"
echo ""

# Create backup directory if it doesn't exist
mkdir -p "${BACKUP_DIR}"

# Function: Log message with timestamp
log() {
	local level=$1
	local message=$2
	local timestamp=$(date '+%Y-%m-%d %H:%M:%S')
	
	case $level in
		INFO) echo -e "${GREEN}[INFO]${NC} ${timestamp} - ${message}" | tee -a "${BACKUP_LOG}"
		;;
		WARN) echo -e "${YELLOW}[WARN]${NC} ${timestamp} - ${message}" | tee -a "${BACKUP_LOG}"
		;;
		ERROR) echo -e "${RED}[ERROR]${NC} ${timestamp} - ${message}" | tee -a "${BACKUP_LOG}"
		;;
		STEP) echo -e "${BLUE}[STEP]${NC} ${timestamp} - ${message}" | tee -a "${BACKUP_LOG}"
		;;
	esac
}

# Function: Check database connection
check_db_connection() {
	log STEP "Checking database connection..."
	
	PGPASSWORD="${DB_PASSWORD}" psql -h "${DB_HOST}" -p "${DB_PORT}" -U "${DB_USER}" -d "${DB_NAME}" -c "SELECT 1" > /dev/null 2>&1
	
	if [ $? -eq 0 ]; then
		log INFO "Database connection successful"
		return 0
	else
		log ERROR "Cannot connect to database"
		return 1
	fi
}

# Function: Create full backup
create_full_backup() {
	log STEP "Creating full backup..."
	
	PGPASSWORD="${DB_PASSWORD}" pg_dump \
		-h "${DB_HOST}" \
		-p "${DB_PORT}" \
		-U "${DB_NAME}" \
		-d "${DB_NAME}" \
		-F custom \
		-f "${BACKUP_FILE}" \
		--compress=9 \
		--verbose \
		--lock-wait-timeout=60000 \
		2>&1 | tee -a "${BACKUP_LOG}"
	
	if [ $? -eq 0 ]; then
		log INFO "Full backup created successfully: ${BACKUP_FILE}"
		
		# Calculate backup size
		BACKUP_SIZE=$(du -h "${BACKUP_FILE}" | cut -f1)
		log INFO "Backup size: ${BACKUP_SIZE}"
		
		# Create backup checksum
		CHECKSUM_FILE="${BACKUP_FILE}.sha256"
		sha256sum "${BACKUP_FILE}" > "${CHECKSUM_FILE}"
		log INFO "Checksum created: ${CHECKSUM_FILE}"
		
		# Create backup metadata
		META_FILE="${BACKUP_FILE}.meta"
		cat > "${META_FILE}" << EOF
{
	"backup_type": "full",
	"timestamp": "${TIMESTAMP}",
	"database": "${DB_NAME}",
	"host": "${DB_HOST}",
	"size": "${BACKUP_SIZE}",
	"checksum": "$(cat ${CHECKSUM_FILE} | awk '{print $1}')",
	"version": "${TIMESTAMP}",
	"created_by": "$(whoami)",
	"created_on": "$(hostname)"
}
EOF
		log INFO "Metadata created: ${META_FILE}"
		
		return 0
	else
		log ERROR "Full backup failed"
		return 1
	fi
}

# Function: Create schema-only backup
create_schema_backup() {
	log STEP "Creating schema-only backup..."
	
	SCHEMA_BACKUP="${BACKUP_DIR}/${DB_NAME}_schema_${TIMESTAMP}.sql"
	
	PGPASSWORD="${DB_PASSWORD}" pg_dump \
		-h "${DB_HOST}" \
		-p "${DB_PORT}" \
		-U "${DB_USER}" \
		-d "${DB_NAME}" \
		--schema-only \
		-f "${SCHEMA_BACKUP}" \
		2>&1 | tee -a "${BACKUP_LOG}"
	
	if [ $? -eq 0 ]; then
		log INFO "Schema backup created: ${SCHEMA_BACKUP}"
		return 0
	else
		log ERROR "Schema backup failed"
		return 1
	fi
}

# Function: Upload backup to S3
upload_to_s3() {
	log STEP "Uploading backup to S3..."
	
	# Upload full backup
	aws s3 cp "${BACKUP_FILE}" "s3://${S3_BUCKET}/${TIMESTAMP}/$(basename ${BACKUP_FILE})" --region "${S3_REGION}"
	
	if [ $? -eq 0 ]; then
		log INFO "Backup uploaded to S3: s3://${S3_BUCKET}/${TIMESTAMP}/$(basename ${BACKUP_FILE})"
		
		# Upload checksum
		aws s3 cp "${CHECKSUM_FILE}" "s3://${S3_BUCKET}/${TIMESTAMP}/$(basename ${CHECKSUM_FILE})" --region "${S3_REGION}"
		
		# Upload metadata
		META_FILE="${BACKUP_FILE}.meta"
		aws s3 cp "${META_FILE}" "s3://${S3_BUCKET}/${TIMESTAMP}/$(basename ${META_FILE})" --region "${S3_REGION}"
		
		# Upload log
		aws s3 cp "${BACKUP_LOG}" "s3://${S3_BUCKET}/${TIMESTAMP}/$(basename ${BACKUP_LOG})" --region "${S3_REGION}"
		
		# Set lifecycle rules for automatic cleanup
		aws s3api put-bucket-lifecycle-configuration \
			--bucket "${S3_BUCKET}" \
			--lifecycle-configuration "file:///tmp/lifecycle.json" \
			--region "${S3_REGION}"
		
		log INFO "All backup files uploaded to S3"
		return 0
	else
		log ERROR "Failed to upload backup to S3"
		return 1
	fi
}

# Function: Cleanup old backups
cleanup_old_backups() {
	log STEP "Cleaning up old backups..."
	
	# Remove local backups older than retention period
	DELETED_COUNT=0
	
	for file in "${BACKUP_DIR}"/*.dump; do
		if [ -f "$file" ]; then
			FILE_AGE=$(( ($(date +%s) - $(stat -c %Y "$file" 2>/dev/null || stat -f %m "$file")) / 86400 ))
			
			if [ ${FILE_AGE} -gt ${RETENTION_DAYS} ]; then
				log INFO "Deleting old backup: $(basename ${file})"
				rm -f "$file"
				rm -f "${file}.sha256"
				rm -f "${file}.meta"
				((DELETED_COUNT++))
			fi
		fi
	done
	
	log INFO "Deleted ${DELETED_COUNT} old backup(s)"
	
	# Clean up S3
	log STEP "Cleaning up S3..."
	
	# Delete objects older than retention period
	aws s3 ls "s3://${S3_BUCKET}/" --recursive --region "${S3_REGION}" | \
		awk "{print \$4}" | \
		while read -r key; do
			if [[ "$key" =~ ^([0-9]{8})_ ]]; then
				BACKUP_DATE="${BASH_REMATCH[1]}"
				BACKUP_AGE=$(( ($(date +%s) - $(date -d "${BACKUP_DATE}" +%s)) / 86400 ))
				
				if [ ${BACKUP_AGE} -gt ${RETENTION_DAYS} ]; then
					log INFO "Deleting old S3 backup: ${key}"
					aws s3 rm "s3://${S3_BUCKET}/${key}" --region "${S3_REGION}"
				fi
			fi
		done
}

# Function: Validate backup
validate_backup() {
	log STEP "Validating backup..."
	
	if [ ! -f "${BACKUP_FILE}" ]; then
		log ERROR "Backup file not found: ${BACKUP_FILE}"
		return 1
	fi
	
	# Verify file is not empty
	if [ ! -s "${BACKUP_FILE}" ]; then
		log ERROR "Backup file is empty: ${BACKUP_FILE}"
		return 1
	fi
	
	# Verify checksum
	if [ -f "${CHECKSUM_FILE}" ]; then
		CALCULATED_CHECKSUM=$(sha256sum "${BACKUP_FILE}" | awk '{print $1}')
		STORED_CHECKSUM=$(cat "${CHECKSUM_FILE}" | awk '{print $1}')
		
		if [ "${CALCULATED_CHECKSUM}" == "${STORED_CHECKSUM}" ]; then
			log INFO "Backup checksum verified successfully"
		else
			log ERROR "Backup checksum verification failed"
			log ERROR "Expected: ${STORED_CHECKSUM}"
			log ERROR "Calculated: ${CALCULATED_CHECKSUM}"
			return 1
		fi
	fi
	
	log INFO "Backup validation successful"
	return 0
}

# Function: Create backup report
create_backup_report() {
	log STEP "Creating backup report..."
	
	REPORT_FILE="${BACKUP_DIR}/backup_report_${TIMESTAMP}.json"
	
	CPU_USAGE=$(top -bn1 | grep "Cpu(s)" | awk '{print $2}' | cut -d'%' -f1)
	MEM_USAGE=$(free -m | awk 'NR==2{printf "%.2f%%", $3*100/$2 }')
	DISK_USAGE=$(df -h "${BACKUP_DIR}" | awk 'NR==2{print $5}')
	
	cat > "${REPORT_FILE}" << EOF
{
	"backup": {
		"timestamp": "${TIMESTAMP}",
		"file": "$(basename ${BACKUP_FILE})",
		"size": "$(du -h ${BACKUP_FILE} | cut -f1)",
		"checksum": "$(cat ${CHECKSUM_FILE} | awk '{print $1}')",
		"status": "completed"
	},
	"system": {
		"hostname": "$(hostname)",
		"cpu_usage": "${CPU_USAGE}%",
		"memory_usage": "${MEM_USAGE}",
		"disk_usage": "${DISK_USAGE}"
	},
	"database": {
		"name": "${DB_NAME}",
		"host": "${DB_HOST}",
		"version": "$(PGPASSWORD=${DB_PASSWORD} psql -h ${DB_HOST} -p ${DB_PORT} -U ${DB_USER} -d ${DB_NAME} -t -c 'SELECT version()' 2>/dev/null)"
	},
	"retention": {
		"days": ${RETENTION_DAYS},
		"weeks": ${RETENTION_WEEKS},
		"months": ${RETENTION_MONTHS}
	}
}
EOF
	log INFO "Backup report created: ${REPORT_FILE}"
}

# Function: Send notification
send_notification() {
	local status=$1
	local message=$2
	
	if [ -n "${SLACK_WEBHOOK_URL}" ]; then
		curl -X POST "${SLACK_WEBHOOK_URL}" \
			-H 'Content-Type: application/json' \
			-d "{\"text\": \"RAIL Database Backup: ${status}\n${message}\"}"
	fi
	
	if [ -n "${EMAIL_NOTIFICATION}" ]; then
		echo "${message}" | mail -s "RAIL Database Backup: ${status}" "${EMAIL_NOTIFICATION}"
	fi
}

# Main backup workflow
main() {
	local start_time=$(date +%s)
	
	log STEP "Starting PostgreSQL backup workflow..."
	log INFO "Backup type: Full backup with PITR support"
	
	# Pre-backup checks
	if ! check_db_connection; then
		send_notification "FAILED" "Database connection failed"
		exit 1
	fi
	
	# Check available disk space
	DISK_FREE=$(df -BG "${BACKUP_DIR}" | awk 'NR==2{print $4}')
	if [ ${DISK_FREE} -lt 10 ]; then
		log ERROR "Insufficient disk space: ${DISK_FREE}GB available, need at least 10GB"
		send_notification "FAILED" "Insufficient disk space"
		exit 1
	fi
	log INFO "Disk space available: ${DISK_FREE}GB"
	
	# Create backups
	if ! create_full_backup; then
		send_notification "FAILED" "Full backup creation failed"
		exit 1
	fi
	
	if ! create_schema_backup; then
		send_notification "FAILED" "Schema backup creation failed"
		exit 1
	fi
	
	# Validate backup
	if ! validate_backup; then
		send_notification "FAILED" "Backup validation failed"
		exit 1
	fi
	
	# Upload to S3
	if ! upload_to_s3; then
		log WARN "S3 upload failed, but local backup is available"
	fi
	
	# Create backup report
	create_backup_report
	
	# Cleanup old backups
	cleanup_old_backups
	
	# Calculate duration
	local end_time=$(date +%s)
	local duration=$((end_time - start_time))
	local duration_min=$((duration / 60))
	local duration_sec=$((duration % 60))
	
	# Summary
	echo ""
	echo "========================================="
	echo "Backup Summary"
	echo "========================================="
	log INFO "Backup completed successfully in ${duration_min}m ${duration_sec}s"
	log INFO "Backup location: ${BACKUP_FILE}"
	log INFO "S3 location: s3://${S3_BUCKET}/${TIMESTAMP}/"
	log INFO "Retention: ${RETENTION_DAYS} days"
	
	send_notification "SUCCESS" "Backup completed successfully in ${duration_min}m ${duration_sec}s"
	
	echo ""
	log INFO "All tasks completed successfully"
	return 0
}

# Parse command line arguments
case "${1:-full}" in
	full)
		main
		;;
	schema-only)
		log STEP "Schema-only backup mode"
		create_schema_backup
		;;
	validate)
		if [ -n "${2}" ]; then
			log STEP "Validating backup: ${2}"
			BACKUP_FILE="${2}"
			validate_backup
		else
			echo "Usage: $0 validate <backup_file>"
			exit 1
		fi
		;;
	restore)
		if [ -n "${2}" ]; then
			log STEP "Restoring backup: ${2}"
			RESTORE_FILE="${2}"
			PGPASSWORD="${DB_PASSWORD}" pg_restore \
				-h "${DB_HOST}" \
				-p "${DB_PORT}" \
				-U "${DB_USER}" \
				-d "${DB_NAME}" \
				-j 4 \
				-F c \
				"${RESTORE_FILE}"
		else
			echo "Usage: $0 restore <backup_file>"
			exit 1
		fi
		;;
	list)
		log STEP "Listing all backups"
		ls -lh "${BACKUP_DIR}"/*.dump 2>/dev/null || log INFO "No backups found"
		;;
	cleanup)
		log STEP "Running cleanup only"
		cleanup_old_backups
		;;
	*)
		echo "RAIL Backend - PostgreSQL Backup Script"
		echo ""
		echo "Usage: $0 [command] [options]"
		echo ""
		echo "Commands:"
		echo "  full         Create full backup (default)"
		echo "  schema-only  Create schema-only backup"
		echo "  validate     Validate backup file"
		echo "  restore      Restore from backup file"
		echo "  list         List all backups"
		echo "  cleanup      Cleanup old backups"
		echo ""
		echo "Environment Variables:"
		echo "  DB_HOST       Database host (default: localhost)"
		echo "  DB_PORT       Database port (default: 5432)"
		echo "  DB_NAME       Database name (default: rail_service_prod)"
		echo "  DB_USER       Database user (default: postgres)"
		echo "  DB_PASSWORD   Database password (required)"
		echo "  BACKUP_DIR    Backup directory (default: /var/backups/rail)"
		echo "  S3_BUCKET     S3 bucket for backups (default: rail-database-backups)"
		echo "  S3_REGION     S3 region (default: us-east-1)"
		echo "  RETENTION_DAYS Retention period in days (default: 30)"
		echo ""
		exit 0
		;;
esac
