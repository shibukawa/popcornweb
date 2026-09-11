package queries

type CreatedRoom { id: int }

type LobbyRoom {
  id: int
  creatorAccountID: string
  creatorDisplayName: string
  title: string
  subject: string
  subjectDescription: string
  status: string
  startingAmount: int
  createdAt: datetime
  participantCount: int
  isCreator: bool
  isParticipant: bool
}

type AuctionRoom {
  id: int
  creatorAccountID: string
  creatorName: string
  title: string
  subject: string
  subjectDescription: string
  status: string
  startingAmount: int
  currentAmount: int
  hasWinningBid: bool
  winningBidderName: string
  createdAt: datetime
  participantCount: int
  viewerIsCreator: bool
  viewerIsParticipant: bool
}

type AuctionEvent {
  id: int
  eventType: string
  amount: int
  authorName: string
  message: string
  createdAt: datetime
}

export statement UpsertAccount(id: string, displayName: string, email: string): sql.exec {
  INSERT INTO accounts (id, display_name, email)
  VALUES ({id}, {displayName}, {email})
  ON CONFLICT
    (id) DO UPDATE SET display_name = excluded.display_name, email = excluded.email, updated_at = CURRENT_TIMESTAMP
}

export statement CreateRoom(creatorAccountID: string, title: string, subject: string, subjectDescription: string, startingAmount: int): sql.one<CreatedRoom> {
  INSERT INTO rooms (creator_account_id, title, subject, subject_description, starting_amount)
  VALUES ({creatorAccountID}, {title}, {subject}, {subjectDescription}, {startingAmount})
  RETURNING id
}

export statement JoinRoom(roomID: int, accountID: string): sql.exec {
  INSERT INTO room_participants (room_id, account_id)
  SELECT id, {accountID}
  FROM rooms
  WHERE id = {roomID} AND status = 'open' AND creator_account_id <> {accountID}
  ON CONFLICT (room_id, account_id) DO NOTHING
}

export statement ListOpenRooms(viewerAccountID: string): sql.many<LobbyRoom> {
  SELECT
    room.id,
    room.creator_account_id AS creatorAccountID,
    creator.display_name AS creatorDisplayName,
    room.title,
    room.subject,
    room.subject_description AS subjectDescription,
    room.status,
    room.starting_amount AS startingAmount,
    room.created_at AS createdAt,
    COUNT(participant.account_id) AS participantCount,
    room.creator_account_id = {viewerAccountID} AS isCreator,
    EXISTS (
      SELECT 1
      FROM room_participants AS viewer_participation
      WHERE
        viewer_participation.room_id = room.id
        AND viewer_participation.account_id = {viewerAccountID}
    ) AS isParticipant
  FROM rooms AS room
  JOIN accounts AS creator
    ON creator.id = room.creator_account_id
  LEFT JOIN room_participants AS participant
    ON participant.room_id = room.id
  WHERE room.status = 'open'
  GROUP BY
    room.id,
    room.creator_account_id,
    creator.display_name,
    room.title,
    room.subject,
    room.subject_description,
    room.status,
    room.starting_amount,
    room.created_at
  ORDER BY room.created_at DESC, room.id DESC
}

export statement GetAuctionRoom(roomID: int, viewerAccountID: string): sql.optional<AuctionRoom> {
  SELECT
    room.id,
    room.creator_account_id AS creatorAccountID,
    creator.display_name AS creatorName,
    room.title,
    room.subject,
    room.subject_description AS subjectDescription,
    room.status,
    room.starting_amount AS startingAmount,
    COALESCE(
      (
        SELECT MAX(history.current_amount)
        FROM auction_history AS history
        WHERE history.room_id = room.id AND history.event_type = 'bid'
      ), room.starting_amount
    ) AS currentAmount,
    EXISTS (
      SELECT 1
      FROM auction_history AS winning_bid
      WHERE winning_bid.room_id = room.id AND winning_bid.event_type = 'bid'
    ) AS hasWinningBid,
    COALESCE(
      (
        SELECT winning_bidder.display_name
        FROM auction_history AS winning_bid
        JOIN accounts AS winning_bidder
          ON winning_bidder.id = winning_bid.bidder_account_id
        WHERE winning_bid.room_id = room.id AND winning_bid.event_type = 'bid'
        ORDER BY winning_bid.current_amount DESC, winning_bid.id DESC
        LIMIT 1
      ), ''
    ) AS winningBidderName,
    room.created_at AS createdAt,
    (
      SELECT COUNT(*)
      FROM room_participants AS participant_count
      WHERE participant_count.room_id = room.id
    ) AS participantCount,
    room.creator_account_id = {viewerAccountID} AS viewerIsCreator,
    EXISTS (
      SELECT 1
      FROM room_participants AS viewer_participation
      WHERE
        viewer_participation.room_id = room.id
        AND viewer_participation.account_id = {viewerAccountID}
    ) AS viewerIsParticipant
  FROM rooms AS room
  JOIN accounts AS creator
    ON creator.id = room.creator_account_id
  WHERE room.id = {roomID}
}

export statement ListAuctionHistory(roomID: int): sql.many<AuctionEvent> {
  SELECT
    history.id,
    history.event_type AS eventType,
    COALESCE(history.current_amount, 0) AS amount,
    author.display_name AS authorName,
    COALESCE(history.message, '') AS message,
    history.created_at AS createdAt
  FROM auction_history AS history
  JOIN accounts AS author
    ON author.id = COALESCE(history.bidder_account_id, history.host_account_id)
  WHERE history.room_id = {roomID}
  ORDER BY history.id ASC
}

export statement PlaceBid(roomID: int, bidderAccountID: string, amount: int): sql.exec {
  INSERT INTO auction_history (room_id, event_type, current_amount, bidder_account_id)
  VALUES ({roomID}, 'bid', {amount}, {bidderAccountID})
}

export statement PostHostMessage(roomID: int, hostAccountID: string, message: string): sql.exec {
  INSERT INTO auction_history (room_id, event_type, host_account_id, message)
  VALUES ({roomID}, 'host_message', {hostAccountID}, {message})
}

export statement CloseAuctionRoom(roomID: int, hostAccountID: string): sql.exec {
  UPDATE rooms
  SET status = 'closed', closed_at = CURRENT_TIMESTAMP
  WHERE id = {roomID} AND creator_account_id = {hostAccountID} AND status = 'open'
}
