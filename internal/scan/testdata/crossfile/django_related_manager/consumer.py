def attendee_emails(order):
    return order.all_positions.filter(attendee_email__isnull=False)
